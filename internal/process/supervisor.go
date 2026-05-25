package process

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"llama-server-studio/internal/profiles"
	"llama-server-studio/internal/storage"
)

type activeProcess struct {
	cmd      *exec.Cmd
	logFile  *os.File
	serverID string
	port     int
	stopped  bool
}

// Supervisor manages child llama-server processes.
type Supervisor struct {
	db     *storage.DB
	active map[string]*activeProcess
	mu     sync.Mutex
}

// NewSupervisor creates a process supervisor instance.
func NewSupervisor(db *storage.DB) *Supervisor {
	return &Supervisor{
		db:     db,
		active: make(map[string]*activeProcess),
	}
}

// AllocatePort finds the first open port within the given range.
func (s *Supervisor) AllocatePort(host string, start, end int) (int, error) {
	// Find ports not currently used by our active processes
	s.mu.Lock()
	usedPorts := make(map[int]bool)
	for _, proc := range s.active {
		usedPorts[proc.port] = true
	}
	s.mu.Unlock()

	for port := start; port <= end; port++ {
		if usedPorts[port] {
			continue
		}
		// Probe the OS port
		addr := net.JoinHostPort(host, strconv.Itoa(port))
		ln, err := net.Listen("tcp", addr)
		if err == nil {
			_ = ln.Close()
			return port, nil
		}
	}
	return 0, fmt.Errorf("no available ports in range %d-%d", start, end)
}

// IsPortAvailable probes if a specific port is open.
func (s *Supervisor) IsPortAvailable(host string, port int) bool {
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

// StartServer launches a child llama-server process based on a profile.
func (s *Supervisor) StartServer(profileID string, configBinPath string, portRangeStart, portRangeEnd int) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 1. Fetch Profile
	p, ok := s.db.GetProfile(profileID)
	if !ok {
		return "", errors.New("profile not found")
	}

	// 2. Ensure no server is already running for this profile
	for _, proc := range s.active {
		if existing, ok := s.db.GetServer(proc.serverID); ok && existing.ProfileID == profileID {
			return "", fmt.Errorf("a server is already active for this profile (ID: %s)", proc.serverID)
		}
	}

	// 3. Fetch Model
	m, ok := s.db.GetModel(p.ModelID)
	if !ok {
		return "", errors.New("associated GGUF model not found in catalog")
	}

	// 4. Determine host and port
	host := p.DefaultHost
	if host == "" {
		host = "127.0.0.1"
	}

	var port int
	var err error
	if p.DefaultPortPolicy == "fixed" {
		port = p.FixedPort
		if !s.IsPortAvailable(host, port) {
			return "", fmt.Errorf("configured fixed port %d is already in use by another application", port)
		}
	} else {
		port, err = s.AllocatePort(host, portRangeStart, portRangeEnd)
		if err != nil {
			return "", err
		}
	}

	// 5. Build executable path (prioritize profile level, then global settings config)
	binPath := configBinPath
	
	// 6. Build command line
	built, err := profiles.BuildCommand(&p, &m, binPath, port)
	if err != nil {
		return "", err
	}

	// 7. Create unique server ID and file logs
	serverID := fmt.Sprintf("srv_%d", time.Now().UnixNano())
	logPath := s.db.GetLogFilePath(serverID)
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return "", fmt.Errorf("failed to create log file: %w", err)
	}

	// Write command-line invocation prefix to the logs
	_, _ = logFile.WriteString("=== LLAMA SERVER STUDIO EXECUTION ===\n")
	_, _ = logFile.WriteString(fmt.Sprintf("Timestamp: %s\n", time.Now().Format(time.RFC3339)))
	_, _ = logFile.WriteString(fmt.Sprintf("Command: %s\n", built.CmdString))
	_, _ = logFile.WriteString("=====================================\n\n")

	// 8. Setup child execution command
	cmd := exec.Command(built.Executable, built.Args...)
	if built.WorkDir != "" {
		cmd.Dir = built.WorkDir
	}
	if len(built.Env) > 0 {
		cmd.Env = append(os.Environ(), built.Env...)
	}
	
	// Multiwriter: pipe logs to both our server log file
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	// Start execution
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return "", fmt.Errorf("failed to execute llama-server: %w", err)
	}

	// Save active record
	proc := &activeProcess{
		cmd:      cmd,
		logFile:  logFile,
		serverID: serverID,
		port:     port,
	}
	s.active[serverID] = proc

	// Store in DB
	nowStr := time.Now().Format(time.RFC3339)
	srvRecord := storage.Server{
		ID:              serverID,
		InstanceID:      serverID,
		ProfileID:       p.ID,
		ProfileIDSpec:   p.ID,
		ModelID:         m.ID,
		PID:             cmd.Process.Pid,
		Host:            host,
		Port:            port,
		BaseURL:         fmt.Sprintf("http://%s:%d", host, port),
		Status:          "starting",
		StartedAt:       nowStr,
		StartedAtSpec:   nowStr,
		ProfileSnapshot: p,
		Argv:            append([]string{built.Executable}, built.Args...),
		LogPath:         logPath,
		Health:          storage.ServerHealth{LastCheckAt: "", State: "unknown"},
		CreatedAt:       nowStr,
		UpdatedAt:       nowStr,
	}
	_ = s.db.SaveServer(srvRecord)

	// Launch background monitor
	go s.monitorProcess(proc, srvRecord)

	return serverID, nil
}

// StopServer triggers a graceful SIGINT and kills process on timeout.
func (s *Supervisor) StopServer(serverID string) error {
	s.mu.Lock()
	proc, exists := s.active[serverID]
	s.mu.Unlock()

	if !exists {
		// Not in active map, but let's check if the database thinks it's still alive
		if srv, ok := s.db.GetServer(serverID); ok && srv.Status != "stopped" && srv.Status != "crashed" {
			srv.Status = "stopped"
			srv.StoppedAt = time.Now().Format(time.RFC3339)
			srv.UpdatedAt = time.Now().Format(time.RFC3339)
			_ = s.db.SaveServer(srv)
		}
		return nil
	}

	proc.stopped = true

	// Immediately update DB to show "stopping" state
	if srv, ok := s.db.GetServer(serverID); ok {
		srv.Status = "stopping"
		srv.UpdatedAt = time.Now().Format(time.RFC3339)
		_ = s.db.SaveServer(srv)
	}

	// Send Interrupt signal to let llama-server save/clean resources
	_ = proc.cmd.Process.Signal(os.Interrupt)

	// Wait in goroutine to kill if it doesn't respond
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		done := make(chan struct{})
		go func() {
			_, _ = proc.cmd.Process.Wait()
			close(done)
		}()

		select {
		case <-done:
			// exited cleanly
		case <-ctx.Done():
			// Force kill
			_ = proc.cmd.Process.Kill()
		}
	}()

	return nil
}

func (s *Supervisor) monitorProcess(proc *activeProcess, srv storage.Server) {
	// 1. Continuous background heartbeat monitor
	healthCtx, healthCancel := context.WithCancel(context.Background())
	defer healthCancel()

	go func() {
		client := http.Client{Timeout: 1 * time.Second}
		healthURL := fmt.Sprintf("http://%s:%d/health", srv.Host, srv.Port)
		modelsURL := fmt.Sprintf("http://%s:%d/v1/models", srv.Host, srv.Port)
		
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()

		isReady := false

		for {
			select {
			case <-healthCtx.Done():
				return
			case <-ticker.C:
				// First check if process is already dead in our supervisor list
				s.mu.Lock()
				_, stillRunning := s.active[proc.serverID]
				s.mu.Unlock()
				if !stillRunning {
					return
				}

				// Check primary health endpoint
				req, _ := http.NewRequestWithContext(healthCtx, "GET", healthURL, nil)
				resp, err := client.Do(req)
				
				checkSuccess := false
				if err == nil {
					resp.Body.Close()
					if resp.StatusCode == http.StatusOK {
						checkSuccess = true
					}
				}

				// If health checked OK but not ready, verify models endpoint
				if checkSuccess && !isReady {
					modelsReq, _ := http.NewRequestWithContext(healthCtx, "GET", modelsURL, nil)
					modelsResp, errModels := client.Do(modelsReq)
					if errModels == nil {
						modelsResp.Body.Close()
						if modelsResp.StatusCode == http.StatusOK {
							isReady = true
							// Slow down check interval once fully ready
							ticker.Reset(3 * time.Second)
						}
					}
				}

				nowStr := time.Now().Format(time.RFC3339)

				// Update Server record in DB
				if currentSrv, ok := s.db.GetServer(proc.serverID); ok {
					currentSrv.Health.LastCheckAt = nowStr
					if checkSuccess {
						if isReady {
							currentSrv.Status = "healthy"
							currentSrv.Health.State = "healthy"
						} else {
							currentSrv.Status = "starting"
							currentSrv.Health.State = "loading"
						}
					} else {
						if isReady {
							currentSrv.Status = "unhealthy"
							currentSrv.Health.State = "unhealthy"
						} else {
							currentSrv.Status = "starting"
							currentSrv.Health.State = "unknown"
						}
					}
					currentSrv.UpdatedAt = nowStr
					_ = s.db.SaveServer(currentSrv)
				}
			}
		}
	}()

	// 2. Wait for process exit
	err := proc.cmd.Wait()

	healthCancel() // stop checking health

	exitCode := 0
	var lastErr string
	if err != nil {
		lastErr = err.Error()
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
			// If it exited with signal (like SIGINT/SIGKILL)
			if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
				if status.Signaled() {
					lastErr = fmt.Sprintf("exited via signal: %s", status.Signal())
				}
			}
		}
	}

	// Log termination details
	_, _ = proc.logFile.WriteString("\n=== PROCESS EXITED ===\n")
	_, _ = proc.logFile.WriteString(fmt.Sprintf("Timestamp: %s\n", time.Now().Format(time.RFC3339)))
	_, _ = proc.logFile.WriteString(fmt.Sprintf("Exit Code: %d\n", exitCode))
	if lastErr != "" {
		_, _ = proc.logFile.WriteString(fmt.Sprintf("Exit Error: %s\n", lastErr))
	}
	_, _ = proc.logFile.WriteString("======================\n")

	_ = proc.logFile.Close()

	// Update DB record
	s.mu.Lock()
	delete(s.active, proc.serverID)
	s.mu.Unlock()

	// If we didn't explicitly trigger the stop, and exitCode != 0, it crashed!
	finalStatus := "stopped"
	if !proc.stopped && exitCode != 0 {
		finalStatus = "crashed"
	}

	s.setServerStatus(proc.serverID, finalStatus, exitCode, lastErr)
}

func (s *Supervisor) setServerStatus(serverID string, status string, exitCode int, errStr string) {
	if srv, ok := s.db.GetServer(serverID); ok {
		// Retain starting -> healthy transitions, but avoid healthy -> starting downgrades
		if (srv.Status == "healthy" || srv.Status == "ready") && status == "starting" {
			return
		}
		srv.Status = status
		if status == "stopped" || status == "crashed" {
			srv.StoppedAt = time.Now().Format(time.RFC3339)
			srv.PID = 0
			srv.ExitCode = exitCode
		}
		if errStr != "" {
			srv.LastError = errStr
		}
		srv.UpdatedAt = time.Now().Format(time.RFC3339)
		_ = s.db.SaveServer(srv)
	}
}

// GetProcessStats captures CPU and Memory of a running process (mocked or simple OS fallback)
func (s *Supervisor) GetProcessStats(serverID string) (float64, int64, error) {
	s.mu.Lock()
	proc, exists := s.active[serverID]
	s.mu.Unlock()

	if !exists || proc.cmd.Process == nil {
		return 0, 0, errors.New("process not active")
	}

	// Standard library does not easily fetch RSS/CPU without os-specific syscalls or /proc parsing.
	// We will implement a lightweight cross-platform parser or a reliable stub fallback that uses
	// ps command on macOS to fetch RSS memory and CPU percentage! This is extremely elegant and standard.
	return fetchOSStats(proc.cmd.Process.Pid)
}

func fetchOSStats(pid int) (float64, int64, error) {
	// Execute 'ps -p <pid> -o %cpu,rss' on mac/linux
	cmd := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "%cpu,rss")
	out, err := cmd.Output()
	if err != nil {
		return 0, 0, err
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 {
		return 0, 0, errors.New("unexpected ps output")
	}

	// Parse fields
	fields := strings.Fields(lines[1])
	if len(fields) < 2 {
		return 0, 0, errors.New("failed parsing ps columns")
	}

	cpu, _ := strconv.ParseFloat(fields[0], 64)
	rssKB, _ := strconv.ParseInt(fields[1], 10, 64)

	return cpu, rssKB * 1024, nil
}
