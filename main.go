package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"llama-server-studio/internal/bench"
	"llama-server-studio/internal/config"
	"llama-server-studio/internal/httpapi"
	"llama-server-studio/internal/models"
	"llama-server-studio/internal/process"
	"llama-server-studio/internal/router"
	"llama-server-studio/internal/stats"
	"llama-server-studio/internal/storage"
)

//go:embed web/*
var embedFS embed.FS

func main() {
	// 1. Setup CLI Flags
	listenFlag := flag.String("listen", "", "Studio bind address (default 127.0.0.1:3100)")
	configFlag := flag.String("config", "", "Path to config.json file")
	dataDirFlag := flag.String("data-dir", "", "Path to data directory")
	serverBinFlag := flag.String("llama-server-bin", "", "Path to llama-server executable")
	binDirFlag := flag.String("llama-bin-dir", "", "Path to llama.cpp binary directory")
	modelsDirFlag := flag.String("models-dir", "", "Add a model scan directory (can be repeated)")
	scanHFFlag := flag.String("scan-hf-cache", "", "Scan Hugging Face cache directories (true/false)")
	adminTokenFlag := flag.String("admin-token", "", "Token required for remote admin access")
	allowLANFlag := flag.String("allow-insecure-lan", "", "Allow non-localhost bind without token (true/false)")

	flag.Parse()

	// 2. Load Configuration File
	cfgPath := *configFlag
	if cfgPath == "" {
		home, _ := os.UserHomeDir()
		cfgPath = filepath.Join(home, ".config", "llama-server-studio", "config.json")
	}

	cfg, err := config.LoadConfig(cfgPath)
	if err != nil {
		log.Fatalf("Failed to initialize configuration: %v", err)
	}

	// 3. Override configs with CLI arguments
	if *listenFlag != "" {
		cfg.Listen = *listenFlag
	}
	if *dataDirFlag != "" {
		cfg.DataDir = *dataDirFlag
	}
	if *serverBinFlag != "" {
		cfg.LlamaServerBin = *serverBinFlag
	}
	if *binDirFlag != "" {
		cfg.LlamaBinDir = *binDirFlag
	}
	if *modelsDirFlag != "" {
		// Split by comma in case multiple are passed, or append
		cfg.ModelsDirs = strings.Split(*modelsDirFlag, ",")
	}
	if *scanHFFlag != "" {
		cfg.ScanHFCache = (*scanHFFlag == "true")
	}
	if *adminTokenFlag != "" {
		cfg.AdminToken = *adminTokenFlag
	}
	if *allowLANFlag != "" {
		cfg.AllowInsecureLAN = (*allowLANFlag == "true")
	}

	// 4. Try auto-detecting llama-server binary in common paths if empty
	if cfg.LlamaServerBin == "" {
		cfg.LlamaServerBin = locateLlamaServer(cfg.LlamaBinDir)
	}

	// === CRITICAL BOOT VALIDATION CHECKS ===
	// A. Validate llama-server executable
	if cfg.LlamaServerBin == "" {
		log.Fatalf("CRITICAL ERROR: No 'llama-server' executable path is configured. Please provide it via '--llama-server-bin' flag or define 'llama_server_bin' in your config.json file.")
	}

	binInfo, err := os.Stat(cfg.LlamaServerBin)
	if err != nil {
		if os.IsNotExist(err) {
			log.Fatalf("CRITICAL ERROR: Configured 'llama-server' executable does not exist at path: %s", cfg.LlamaServerBin)
		}
		log.Fatalf("CRITICAL ERROR: Failed to read 'llama-server' binary metadata: %v", err)
	}

	if binInfo.IsDir() {
		log.Fatalf("CRITICAL ERROR: The configured 'llama-server' path is a directory, not a executable file: %s", cfg.LlamaServerBin)
	}

	// Verify execution permissions
	if binInfo.Mode()&0111 == 0 {
		log.Fatalf("CRITICAL ERROR: The configured 'llama-server' file at '%s' is not executable. Please run 'chmod +x %s' to grant execution permissions.", cfg.LlamaServerBin, cfg.LlamaServerBin)
	}

	// Verify runnable execution (dry run)
	dryCmd := exec.Command(cfg.LlamaServerBin, "--help")
	if err := dryCmd.Start(); err != nil {
		log.Fatalf("CRITICAL ERROR: The configured file at '%s' failed execution checks (binary may be corrupted or of incompatible processor architecture): %v", cfg.LlamaServerBin, err)
	} else {
		// Wait a moment and kill dry run safely
		go func() {
			time.Sleep(100 * time.Millisecond)
			_ = dryCmd.Process.Kill()
		}()
		_ = dryCmd.Wait()
	}

	// B. Validate models scan directories
	if len(cfg.ModelsDirs) == 0 {
		log.Fatalf("CRITICAL ERROR: No model scan directories configured. Please specify at least one scan directory using the '--models-dir' flag or define 'models_dirs' in your config.json file.")
	}

	validDirCount := 0
	for _, dir := range cfg.ModelsDirs {
		// Attempt to create directory if missing
		if err := os.MkdirAll(dir, 0755); err != nil {
			log.Printf("Warning: Configured model scan directory '%s' could not be resolved or created: %v", dir, err)
			continue
		}
		validDirCount++
	}

	if validDirCount == 0 {
		log.Fatalf("CRITICAL ERROR: None of the configured model scan directories are accessible or exist.")
	}
	// =======================================

	// Save configuration back with resolved details
	_ = config.SaveConfig(cfg, cfgPath)

	// 5. Initialize Storage Database
	db, err := storage.Open(cfg.DataDir)
	if err != nil {
		log.Fatalf("Failed to open storage system: %v", err)
	}

	// 6. Initialize core services
	supervisor := process.NewSupervisor(db)
	benchRunner := bench.NewRunner(db, supervisor)
	proxyRouter := router.NewRouter(db, supervisor)

	// 7. Background Context for Workers
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start CPU/RAM resource telemetry monitoring in background
	go stats.StartMonitor(ctx, db, supervisor)

	// Trigger initial models directories scan in background on boot
	go func() {
		log.Println("[Scanner] Starting GGUF model files crawl...")
		_ = models.ScanDirectories(db, cfg.ModelsDirs, cfg.ScanHFCache, cfg.HFCacheDirs)
		log.Printf("[Scanner] Crawl finished. Model Catalog is ready.")
	}()

	// 8. Register HTTP API Routes
	mux := http.NewServeMux()
	apiServer := httpapi.NewServer(db, supervisor, benchRunner, proxyRouter, cfg, cfgPath, embedFS)
	apiServer.RegisterRoutes(mux)

	// 9. Startup Web Server Listeners
	go func() {
		printBanner(cfg)
		if err := http.ListenAndServe(cfg.Listen, mux); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Failed to bind listen address: %v", err)
		}
	}()

	// 10. Handle OS Interrupt Shutdown signals
	stopChan := make(chan os.Signal, 1)
	signal.Notify(stopChan, os.Interrupt, syscall.SIGTERM)
	<-stopChan

	log.Println("Shutting down llama-server-studio...")
	cancel() // terminate background monitoring goroutines
	
	// Terminate any remaining active child processes gracefully
	for _, srv := range db.ListServers() {
		if srv.Status == "healthy" || srv.Status == "starting" {
			log.Printf("Stopping active child process PID %d...", srv.PID)
			_ = supervisor.StopServer(srv.ID)
		}
	}
	
	log.Println("Studio exited cleanly.")
}

func locateLlamaServer(binDir string) string {
	// 1. If a custom binary directory is configured, check it first
	if binDir != "" {
		path := filepath.Join(binDir, "llama-server")
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path
		}
	}

	// 2. Look up in System PATH first
	if path, err := exec.LookPath("llama-server"); err == nil {
		return path
	}

	// Check common installation folder paths
	home, _ := os.UserHomeDir()
	candidates := []string{
		"/usr/local/bin/llama-server",
		"/opt/homebrew/bin/llama-server",
		filepath.Join(home, "llama.cpp", "build", "bin", "llama-server"),
		filepath.Join(home, "llama.cpp", "llama-server"),
	}

	for _, path := range candidates {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path
		}
	}

	return "" // Must be configured via UI/CLI settings
}

func printBanner(cfg *config.Config) {
	fmt.Println(` `)
	fmt.Println(`  🦙 L L A M A   S E R V E R   S T U D I O`)
	fmt.Println(`  =========================================`)
	fmt.Printf("  Listen Endpoint : http://%s\n", cfg.Listen)
	fmt.Printf("  Data Directory  : %s\n", cfg.DataDir)
	if cfg.LlamaServerBin != "" {
		fmt.Printf("  Binary Location : %s\n", cfg.LlamaServerBin)
	} else {
		fmt.Println("  Binary Location : [!] NOT FOUND (Configure path via Web UI Settings)")
	}
	fmt.Printf("  Scan Folders    : %s\n", strings.Join(cfg.ModelsDirs, ", "))
	
	if cfg.Listen != "127.0.0.1:3100" && !cfg.AllowInsecureLAN && cfg.AdminToken != "" {
		fmt.Printf("  Admin Token     : %s\n", cfg.AdminToken)
	}
	fmt.Println(`  =========================================`)
	fmt.Println(` `)
}
