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

	"llama-server-studio/internal/config"
	"llama-server-studio/internal/profiles"
	"llama-server-studio/internal/storage"
)

type srvUpdate struct {
	status   string
	state    string
	exitCode int
	lastErr  string
}

type activeProcess struct {
	cmd        *exec.Cmd
	logFile    *os.File
	serverID   string
	port       int
	stopped    bool
	reattached bool
	updates    chan srvUpdate
}

// Supervisor manages child llama-server processes.
type Supervisor struct {
	db     *storage.DB
	cfg    *config.Config
	active map[string]*activeProcess
	mu     sync.Mutex
}

// NewSupervisor creates a process supervisor instance.
func NewSupervisor(db *storage.DB, cfg *config.Config) *Supervisor {
	return &Supervisor{
		db:     db,
		cfg:    cfg,
		active: make(map[string]*activeProcess),
	}
}

// Expose thread-safe IsPortAvailable for external callers.
func (s *Supervisor) IsPortAvailable(host string, port int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.isPortAvailableLocked(host, port)
}

// Internal locked check that also respects supervisor's own reservations.
func (s *Supervisor) isPortAvailableLocked(host string, port int) bool {
	for _, p := range s.active {
		if p.port == port {
			return false
		}
	}
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

func (s *Supervisor) allocatePortLocked(host string, start, end int) (int, error) {
	for port := start; port <= end; port++ {
		if s.isPortAvailableLocked(host, port) {
			return port, nil
		}
	}
	return 0, fmt.Errorf("no available ports in range %d-%d", start, end)
}

func (s *Supervisor) discardReservation(id string) {
	s.mu.Lock()
	delete(s.active, id)
	s.mu.Unlock()
}

// StartServer launches a child llama-server process with a narrow lock scope.
func (s *Supervisor) StartServer(profileID string, configBinPath string, portRangeStart, portRangeEnd int) (string, error) {
	// --- Phase 1: Read-only DB lookups, no lock held ---
	p, ok := s.db.GetProfile(profileID)
	if !ok {
		return "", errors.New("profile not found")
	}
	m, ok := s.db.GetModel(p.ModelID)
	if !ok {
		return "", errors.New("associated GGUF model not found in catalog")
	}

	host := p.DefaultHost
	if host == "" {
		host = "127.0.0.1"
	}

	// --- Phase 2: Short critical section for port allocation + reservation ---
	serverID := fmt.Sprintf("srv_%d", time.Now().UnixNano())
	var port int
	var err error

	s.mu.Lock()
	// Prevent duplicate starts for the same profile
	for _, proc := range s.active {
		if existing, ok := s.db.GetServer(proc.serverID); ok && existing.ProfileID == profileID {
			s.mu.Unlock()
			return "", fmt.Errorf("a server is already active for this profile (ID: %s)", proc.serverID)
		}
	}

	if p.DefaultPortPolicy == "fixed" {
		port = p.FixedPort
		if !s.isPortAvailableLocked(host, port) {
			s.mu.Unlock()
			return "", fmt.Errorf("configured fixed port %d is already in use by another application", port)
		}
	} else {
		port, err = s.allocatePortLocked(host, portRangeStart, portRangeEnd)
		if err != nil {
			s.mu.Unlock()
			return "", err
		}
	}

	// Reserve port/process slot to avoid races
	proc := &activeProcess{
		serverID: serverID,
		port:     port,
		updates:  make(chan srvUpdate, 32),
	}
	s.active[serverID] = proc
	s.mu.Unlock()

	// --- Phase 3: Exec and filesystem operations, no lock held ---
	built, err := profiles.BuildCommand(&p, &m, configBinPath, port)
	if err != nil {
		s.discardReservation(serverID)
		return "", err
	}

	logPath := s.db.GetLogFilePath(serverID)
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		s.discardReservation(serverID)
		return "", fmt.Errorf("failed to create log file: %w", err)
	}

	_, _ = logFile.WriteString("=== LLAMA SERVER STUDIO EXECUTION ===\n")
	_, _ = logFile.WriteString(fmt.Sprintf("Timestamp: %s\n", time.Now().Format(time.RFC3339)))
	_, _ = logFile.WriteString(fmt.Sprintf("Command: %s\n", built.CmdString))
	_, _ = logFile.WriteString("=====================================\n\n")

	cmd := exec.Command(built.Executable, built.Args...)
	if built.WorkDir != "" {
		cmd.Dir = built.WorkDir
	}
	if len(built.Env) > 0 {
		cmd.Env = append(os.Environ(), built.Env...)
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = newSysProcAttr() // pgid/pdeathsig integration

	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		s.discardReservation(serverID)
		return "", fmt.Errorf("failed to execute llama-server: %w", err)
	}

	// --- Phase 4: Promote active process with cmd and logFile handle ---
	s.mu.Lock()
	proc.cmd = cmd
	proc.logFile = logFile
	s.mu.Unlock()

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

	// Serialize DB updates through one goroutine
	go s.serializeDBUpdates(proc)

	// Launch background monitor
	go s.monitorProcess(proc, srvRecord)

	return serverID, nil
}

// Reattach registers an orphan process from a previous studio run.
func (s *Supervisor) Reattach(serverID string) error {
	srv, ok := s.db.GetServer(serverID)
	if !ok {
		return errors.New("server not found in DB")
	}

	s.mu.Lock()
	// Ensure not already reattached or running
	if _, exists := s.active[serverID]; exists {
		s.mu.Unlock()
		return nil
	}

	logFile, err := os.OpenFile(srv.LogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		s.mu.Unlock()
		return fmt.Errorf("failed to open reattached log: %w", err)
	}

	proc := &activeProcess{
		logFile:    logFile,
		serverID:   serverID,
		port:       srv.Port,
		reattached: true,
		updates:    make(chan srvUpdate, 32),
	}
	s.active[serverID] = proc
	s.mu.Unlock()

	_, _ = logFile.WriteString(fmt.Sprintf("\n=== REATTACHED ORPHAN INSTANCE ===\nReattached At: %s\n==================================\n\n", time.Now().Format(time.RFC3339)))

	go s.serializeDBUpdates(proc)
	go s.monitorProcess(proc, srv)

	return nil
}

// StopServer triggers a graceful SIGINT and kills process on timeout.
func (s *Supervisor) StopServer(serverID string) error {
	s.mu.Lock()
	proc, exists := s.active[serverID]
	s.mu.Unlock()

	if !exists {
		// Not in active map, but check if DB thinks it's still alive
		if srv, ok := s.db.GetServer(serverID); ok && srv.Status != "stopped" && srv.Status != "crashed" {
			srv.Status = "stopped"
			srv.StoppedAt = time.Now().Format(time.RFC3339)
			srv.UpdatedAt = time.Now().Format(time.RFC3339)
			_ = s.db.SaveServer(srv)
		}
		return nil
	}

	proc.stopped = true

	// If we are reattached and don't have a direct *exec.Cmd handle, we use signal by PID
	if proc.reattached || proc.cmd == nil {
		if srv, ok := s.db.GetServer(serverID); ok && srv.PID > 0 {
			proc.updates <- srvUpdate{status: "stopping"}
			p, err := os.FindProcess(srv.PID)
			if err == nil {
				_ = p.Signal(os.Interrupt)
				go func() {
					// wait up to 10s then kill
					time.Sleep(10 * time.Second)
					s.mu.Lock()
					_, active := s.active[serverID]
					s.mu.Unlock()
					if active {
						_ = p.Kill()
					}
				}()
			}
		}
		return nil
	}

	proc.updates <- srvUpdate{status: "stopping"}

	// Send Interrupt signal to process group if pgid is available, otherwise process itself
	if pgid, err := syscall.Getpgid(proc.cmd.Process.Pid); err == nil {
		_ = syscall.Kill(-pgid, syscall.SIGINT)
	} else {
		_ = proc.cmd.Process.Signal(os.Interrupt)
	}

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
			// SIGKILL the entire process group if possible
			if pgid, err := syscall.Getpgid(proc.cmd.Process.Pid); err == nil {
				_ = syscall.Kill(-pgid, syscall.SIGKILL)
			} else {
				_ = proc.cmd.Process.Kill()
			}
		}
	}()

	return nil
}

func (s *Supervisor) serializeDBUpdates(proc *activeProcess) {
	for u := range proc.updates {
		if cur, ok := s.db.GetServer(proc.serverID); ok {
			if (cur.Status == "healthy" || cur.Status == "ready") && u.status == "starting" {
				continue
			}
			if u.status != "" {
				cur.Status = u.status
			}
			if u.state != "" {
				cur.Health.State = u.state
				cur.Health.LastCheckAt = time.Now().Format(time.RFC3339)
			}
			if u.status == "stopped" || u.status == "crashed" {
				cur.StoppedAt = time.Now().Format(time.RFC3339)
				cur.PID = 0
				cur.ExitCode = u.exitCode
				cur.LastError = u.lastErr
			}
			cur.UpdatedAt = time.Now().Format(time.RFC3339)
			_ = s.db.SaveServer(cur)
		}
	}
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
				s.mu.Lock()
				_, stillRunning := s.active[proc.serverID]
				s.mu.Unlock()
				if !stillRunning {
					return
				}

				req, _ := http.NewRequestWithContext(healthCtx, "GET", healthURL, nil)
				resp, err := client.Do(req)
				
				checkSuccess := false
				if err == nil {
					resp.Body.Close()
					if resp.StatusCode == http.StatusOK {
						checkSuccess = true
					}
				}

				if checkSuccess && !isReady {
					modelsReq, _ := http.NewRequestWithContext(healthCtx, "GET", modelsURL, nil)
					modelsResp, errModels := client.Do(modelsReq)
					if errModels == nil {
						modelsResp.Body.Close()
						if modelsResp.StatusCode == http.StatusOK {
							isReady = true
							ticker.Reset(3 * time.Second)
						}
					}
				}

				if checkSuccess {
					if isReady {
						proc.updates <- srvUpdate{status: "healthy", state: "healthy"}
					} else {
						proc.updates <- srvUpdate{status: "starting", state: "loading"}
					}
				} else {
					if isReady {
						proc.updates <- srvUpdate{status: "unhealthy", state: "unhealthy"}
					} else {
						proc.updates <- srvUpdate{status: "starting", state: "unknown"}
					}
				}
			}
		}
	}()

	var exitCode int
	var lastErr string

	// 2. Wait for process exit
	if proc.reattached {
		// Reattached orphans don't have exec.Cmd child parent linkage, poll via Signal(0)
		for {
			alive := false
			p, err := os.FindProcess(srv.PID)
			if err == nil {
				alive = p.Signal(syscall.Signal(0)) == nil
			}
			if !alive {
				break
			}
			time.Sleep(500 * time.Millisecond)
		}
	} else {
		err := proc.cmd.Wait()
		if err != nil {
			lastErr = err.Error()
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				exitCode = exitErr.ExitCode()
				if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
					if status.Signaled() {
						lastErr = fmt.Sprintf("exited via signal: %s", status.Signal())
					}
				}
			}
		}
	}

	healthCancel()

	if proc.logFile != nil {
		_, _ = proc.logFile.WriteString("\n=== PROCESS EXITED ===\n")
		_, _ = proc.logFile.WriteString(fmt.Sprintf("Timestamp: %s\n", time.Now().Format(time.RFC3339)))
		_, _ = proc.logFile.WriteString(fmt.Sprintf("Exit Code: %d\n", exitCode))
		if lastErr != "" {
			_, _ = proc.logFile.WriteString(fmt.Sprintf("Exit Error: %s\n", lastErr))
		}
		_, _ = proc.logFile.WriteString("======================\n")
		_ = proc.logFile.Close()
	}

	s.mu.Lock()
	delete(s.active, proc.serverID)
	s.mu.Unlock()

	finalStatus := "stopped"
	if !proc.stopped && exitCode != 0 {
		finalStatus = "crashed"
	}

	proc.updates <- srvUpdate{status: finalStatus, exitCode: exitCode, lastErr: lastErr}
	// Close update channel to exit serializeDBUpdates loop cleanly
	close(proc.updates)
}

// GetProcessStats captures CPU and Memory of a running process.
func (s *Supervisor) GetProcessStats(serverID string) (float64, int64, error) {
	s.mu.Lock()
	proc, exists := s.active[serverID]
	s.mu.Unlock()

	var targetPID int
	if exists && proc.cmd != nil && proc.cmd.Process != nil {
		targetPID = proc.cmd.Process.Pid
	} else {
		srv, ok := s.db.GetServer(serverID)
		if !ok || srv.PID <= 0 {
			return 0, 0, errors.New("process not active")
		}
		targetPID = srv.PID
	}

	return fetchOSStats(targetPID)
}

func fetchOSStats(pid int) (float64, int64, error) {
	cmd := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "%cpu,rss")
	out, err := cmd.Output()
	if err != nil {
		return 0, 0, err
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 {
		return 0, 0, errors.New("unexpected ps output")
	}

	fields := strings.Fields(lines[1])
	if len(fields) < 2 {
		return 0, 0, errors.New("failed parsing ps columns")
	}

	cpu, _ := strconv.ParseFloat(fields[0], 64)
	rssKB, _ := strconv.ParseInt(fields[1], 10, 64)

	return cpu, rssKB * 1024, nil
}
