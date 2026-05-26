package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/term"

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
	listenFlag      := flag.String("listen", "", "Studio bind address (default 127.0.0.1:3100)")
	configFlag      := flag.String("config", "", "Path to config.json file")
	dataDirFlag     := flag.String("data-dir", "", "Path to data directory (default ~/.llama-server-studio)")
	serverBinFlag   := flag.String("llama-server-bin", "", "Path to llama-server executable")
	binDirFlag      := flag.String("llama-bin-dir", "", "Path to llama.cpp binary directory")
	modelsDirFlag   := flag.String("models-dir", "", "Models scan directory path (defaults to <data-dir>/models)")
	scanHFFlag      := flag.String("scan-hf-cache", "", "Scan Hugging Face cache directories (true/false)")
	allowLANFlag    := flag.String("allow-insecure-lan", "", "Allow non-localhost bind without token (true/false)")
	passwordFlag    := flag.Bool("password", false, "Set the admin password interactively, save bcrypt hash to config.json, and exit")

	flag.Parse()

	// 1b. Resolve data-dir: default to ~/.llama-server-studio
	if *dataDirFlag == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			log.Fatalf("CRITICAL ERROR: Cannot determine home directory for default --data-dir: %v", err)
		}
		*dataDirFlag = filepath.Join(home, ".llama-server-studio")
	}

	// 2. Load Configuration File
	cfgPath := *configFlag
	if cfgPath == "" {
		cfgPath = filepath.Join(*dataDirFlag, "config.json")
	}

	cfg, err := config.LoadConfig(cfgPath)
	if err != nil {
		log.Fatalf("Failed to initialize configuration: %v", err)
	}

	// 2b. -password mode: prompt, hash, save, exit — must run before anything else.
	if *passwordFlag {
		if err := runPasswordSetup(cfg, cfgPath); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	log.Printf("Loaded configuration from: %s", cfgPath)

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
	
	// Single models dir setup, defaults to <data-dir>/models
	modelsDir := *modelsDirFlag
	if modelsDir == "" {
		modelsDir = filepath.Join(cfg.DataDir, "models")
	}
	cfg.ModelsDirs = []string{modelsDir}

	if *scanHFFlag != "" {
		cfg.ScanHFCache = (*scanHFFlag == "true")
	}
	if *allowLANFlag != "" {
		cfg.AllowInsecureLAN = (*allowLANFlag == "true")
	}

	// 3b. Compute loopback flag after all CLI overrides are applied
	cfg.SetBindIsLoopback(computeLoopback(cfg.Listen))

	// 3c. Hugging Face Token setup & rate-limit check
	models.SetHFToken(cfg.HFToken)
	activeHFToken := cfg.HFToken
	if activeHFToken == "" {
		activeHFToken = os.Getenv("HF_TOKEN")
	}
	if activeHFToken == "" {
		activeHFToken = os.Getenv("HF_API_TOKEN")
	}
	if activeHFToken == "" {
		log.Println("WARNING: Hugging Face authentication token (HF_TOKEN env or 'hf_token' in config.json) is missing. Anonymous downloads and metadata queries are highly rate-limited by Hugging Face and may fail or download slower.")
	}

	// 3c. Security guard: non-loopback bind without any admin credential must be explicit
	if !cfg.BindIsLoopback() && !cfg.AllowInsecureLAN && !cfg.HasAdminCredential() {
		log.Fatalf("SECURITY ERROR: Server is configured to listen on %s (non-loopback) but no admin credential is set.\n"+
			"  Run with -password to set a bcrypt password,\n"+
			"  or pass --allow-insecure-lan=true to explicitly opt out.\n"+
			"  Refusing to start to protect against unauthenticated remote access.", cfg.Listen)
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
		cfg.ModelsDirs = []string{filepath.Join(cfg.DataDir, "models")}
	}

	// Always include the HF download root so files dropped by the new
	// Hugging Face Hub downloader (docs/hf_support.md §4) get picked up on
	// the next rescan even if the user reconfigured models_dirs.  Added in
	// memory only — not persisted to config.json.
	hfRoot := cfg.HFDownloadRoot()
	alreadyScanned := false
	for _, d := range cfg.ModelsDirs {
		if filepath.Clean(d) == filepath.Clean(hfRoot) {
			alreadyScanned = true
			break
		}
	}
	if !alreadyScanned {
		cfg.ModelsDirs = append(cfg.ModelsDirs, hfRoot)
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
	supervisor := process.NewSupervisor(db, cfg)
	benchRunner := bench.NewRunner(db, supervisor)
	proxyRouter := router.NewRouter(db, supervisor, cfg)

	// Reattach orphaned processes from previous run
	for _, srv := range db.ListServers() {
		if srv.PID > 0 && srv.Status != "stopped" && srv.Status != "crashed" {
			if err := supervisor.Reattach(srv.ID); err != nil {
				log.Printf("[Orphan] Failed to reattach process %s: %v", srv.ID, err)
			} else {
				log.Printf("[Orphan] Successfully reattached active process %s (PID: %d)", srv.ID, srv.PID)
			}
		}
	}

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

	// 9. Startup Web Server Listeners (Main API & WebUI)
	go func() {
		printBanner(cfg)
		log.Printf("Main Studio Server starting on http://%s", cfg.Listen)
		if err := http.ListenAndServe(cfg.Listen, mux); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Failed to bind main listen address: %v", err)
		}
	}()

	// 9b. Startup API Gateway Server (Listen on main port + 1)
	host, port, err := parseListenAddress(cfg.Listen)
	var gatewayListen string
	if err == nil {
		gatewayListen = net.JoinHostPort(host, strconv.Itoa(port+1))
	} else {
		gatewayListen = "127.0.0.1:3101"
	}

	gatewayMux := http.NewServeMux()
	apiServer.RegisterGatewayRoutes(gatewayMux)

	go func() {
		log.Printf("Public API Gateway Server starting on http://%s", gatewayListen)
		if err := http.ListenAndServe(gatewayListen, gatewayMux); err != nil && err != http.ErrServerClosed {
			log.Printf("[Gateway] Failed to bind gateway listen address %s: %v", gatewayListen, err)
		}
	}()

	// 10. Handle OS Interrupt Shutdown signals
	stopChan := make(chan os.Signal, 1)
	signal.Notify(stopChan, os.Interrupt, syscall.SIGTERM)
	<-stopChan

	log.Println("Shutting down llama-server-studio...")
	cancel() // terminate background monitoring goroutines
	
	// Terminate any remaining active child processes gracefully in parallel
	var stopWG sync.WaitGroup
	for _, srv := range db.ListServers() {
		if srv.Status == "healthy" || srv.Status == "starting" || srv.Status == "stopping" {
			stopWG.Add(1)
			go func(id string, pid int) {
				defer stopWG.Done()
				log.Printf("Stopping active child process PID %d...", pid)
				_ = supervisor.StopServer(id)
			}(srv.ID, srv.PID)
		}
	}

	doneChan := make(chan struct{})
	go func() {
		stopWG.Wait()
		close(doneChan)
	}()

	select {
	case <-doneChan:
		log.Println("All child processes exited cleanly.")
	case <-time.After(15 * time.Second):
		log.Println("Timeout reached waiting for children to shut down; forcing exit.")
	}
	
	log.Println("Studio exited cleanly.")
}

func locateLlamaServer(binDir string) string {
	// 1. If a custom binary directory is configured, check it first
	if binDir != "" {
		candidates := []string{
			filepath.Join(binDir, "llama-server"),
			filepath.Join(binDir, "server"),
		}
		for _, path := range candidates {
			if info, err := os.Stat(path); err == nil && !info.IsDir() {
				return path
			}
		}
	}

	// 2. Look up in System PATH first
	if path, err := exec.LookPath("llama-server"); err == nil {
		return path
	}
	if path, err := exec.LookPath("server"); err == nil {
		return path
	}

	// Check common installation folder paths
	home, _ := os.UserHomeDir()
	candidates := []string{
		"/usr/local/bin/llama-server",
		"/opt/homebrew/bin/llama-server",
		filepath.Join(home, "llama.cpp", "build", "bin", "llama-server"),
		filepath.Join(home, "llama.cpp", "build", "bin", "server"),
		filepath.Join(home, "llama.cpp", "llama-server"),
		filepath.Join(home, "llama.cpp", "server"),
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

	if cfg.AdminPasswordHash != "" {
		fmt.Println("  Admin Auth      : bcrypt password (set via -password)")
	} else if cfg.AdminToken != "" {
		// Print a redacted preview — never expose the full token in stdout
		preview := cfg.AdminToken
		if len(preview) > 8 {
			preview = preview[:4] + "****" + preview[len(preview)-4:]
		} else {
			preview = "****"
		}
		fmt.Printf("  Admin Token     : %s (redacted, consider upgrading to -password)\n", preview)
	} else if !cfg.BindIsLoopback() {
		fmt.Println("  Admin Auth      : [!] NOT SET — remote access requires a credential")
	}
	fmt.Println(`  =========================================`)
	fmt.Println(` `)
}

// computeLoopback returns true when every IP the host resolves to is a loopback address.
func computeLoopback(listen string) bool {
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		return false
	}
	// Unspecified bind (0.0.0.0 / [::]) is NOT loopback — it binds all interfaces.
	if host == "" || host == "0.0.0.0" || host == "::" {
		return false
	}
	// Explicit loopback names
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return true
	}
	// Resolve hostname; all resulting IPs must be loopback
	ips, err := net.LookupIP(host)
	if err != nil || len(ips) == 0 {
		return false
	}
	for _, ip := range ips {
		if !ip.IsLoopback() {
			return false
		}
	}
	return true
}

// runPasswordSetup prompts the user twice for a password, hashes it with bcrypt,
// saves the hash to config.json, and returns.  Called when -password flag is set.
func runPasswordSetup(cfg *config.Config, cfgPath string) error {
	fmt.Println("=== llama-server-studio password setup ===")
	fmt.Printf("Config file: %s\n\n", cfgPath)

	// Read password with no echo using golang.org/x/term
	fmt.Fprint(os.Stderr, "Enter new admin password: ")
	pw1, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr) // newline after hidden input
	if err != nil {
		return fmt.Errorf("failed to read password: %w", err)
	}
	if len(pw1) == 0 {
		return fmt.Errorf("password must not be empty")
	}
	if len(pw1) < 8 {
		return fmt.Errorf("password must be at least 8 characters")
	}

	fmt.Fprint(os.Stderr, "Confirm admin password: ")
	pw2, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return fmt.Errorf("failed to read confirmation: %w", err)
	}

	if string(pw1) != string(pw2) {
		return fmt.Errorf("passwords do not match")
	}

	// Hash with bcrypt and clear the legacy plaintext token
	if err := cfg.HashPassword(string(pw1)); err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}

	if err := config.SaveConfig(cfg, cfgPath); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	fmt.Println("\nPassword set successfully.")
	fmt.Printf("Hash stored in: %s\n", cfgPath)
	fmt.Println("Restart llama-server-studio to apply the new credential.")
	return nil
}

func parseListenAddress(listen string) (string, int, error) {
	host, portStr, err := net.SplitHostPort(listen)
	if err != nil {
		if strings.HasPrefix(listen, ":") {
			port, err := strconv.Atoi(listen[1:])
			if err != nil {
				return "", 0, err
			}
			return "0.0.0.0", port, nil
		}
		return "", 0, err
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return "", 0, err
	}
	return host, port, nil
}
