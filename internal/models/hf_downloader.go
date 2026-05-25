package models

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"llama-server-studio/internal/storage"
)

type DownloadStatus struct {
	ModelString string  `json:"model_string"`
	FileName    string  `json:"file_name"`
	Progress    float64 `json:"progress"` // 0 to 100
	Status      string  `json:"status"`   // pending, downloading, completed, failed
	Error       string  `json:"error,omitempty"`
	BytesLoaded int64   `json:"bytes_loaded"`
	BytesTotal  int64   `json:"bytes_total"`
}

var (
	activeDownloads   = make(map[string]*DownloadStatus)
	activeDownloadsMu sync.RWMutex
)

func GetActiveDownloads() []DownloadStatus {
	activeDownloadsMu.RLock()
	defer activeDownloadsMu.RUnlock()
	list := make([]DownloadStatus, 0, len(activeDownloads))
	for _, status := range activeDownloads {
		list = append(list, *status)
	}
	return list
}

func StartModelDownload(modelStr string, targetDir string, db *storage.DB) (string, error) {
	// Parse model_string e.g. "HauhauCS/Qwen3.6-35B-A3B-Uncensored-HauhauCS-Aggressive:Q4_K_M"
	parts := strings.Split(modelStr, ":")
	if len(parts) < 1 || strings.TrimSpace(parts[0]) == "" {
		return "", errors.New("invalid model string format. Must be repo_id:quantization_suffix")
	}
	repoID := strings.TrimSpace(parts[0])
	quant := "Q4_K_M"
	if len(parts) >= 2 {
		quant = strings.TrimSpace(parts[1])
	}

	// Fetch file list from Hugging Face model API
	url := fmt.Sprintf("https://huggingface.co/api/models/%s", repoID)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create API request: %v", err)
	}

	// Support Hugging Face token authentication for higher API rate limits
	hfToken := os.Getenv("HF_TOKEN")
	if hfToken == "" {
		hfToken = os.Getenv("HF_API_TOKEN")
	}
	hfToken = strings.TrimSpace(hfToken)
	if hfToken != "" {
		req.Header.Set("Authorization", "Bearer "+hfToken)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to reach HuggingFace API: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return "", fmt.Errorf("huggingface repository not found: %s", repoID)
	} else if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("huggingface API returned status %d", resp.StatusCode)
	}

	var hfInfo struct {
		Siblings []struct {
			Rfilename string `json:"rfilename"`
		} `json:"siblings"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&hfInfo); err != nil {
		return "", fmt.Errorf("failed to decode HuggingFace API response: %v", err)
	}

	// Search for a sibling file that matches the quant and ends with .gguf
	var matchFile string
	for _, sib := range hfInfo.Siblings {
		f := sib.Rfilename
		if strings.HasSuffix(strings.ToLower(f), ".gguf") && strings.Contains(strings.ToLower(f), strings.ToLower(quant)) {
			matchFile = f
			break
		}
	}

	// Fallback check: if no files match the quant, check if there's any singular GGUF file in the repo
	if matchFile == "" {
		for _, sib := range hfInfo.Siblings {
			f := sib.Rfilename
			if strings.HasSuffix(strings.ToLower(f), ".gguf") {
				matchFile = f
				break
			}
		}
	}

	if matchFile == "" {
		return "", fmt.Errorf("no GGUF file matching quantization '%s' found in repository", quant)
	}

	// Construct download URL
	downloadURL := fmt.Sprintf("https://huggingface.co/%s/resolve/main/%s", repoID, matchFile)

	activeDownloadsMu.Lock()
	for _, s := range activeDownloads {
		if s.Status == "pending" || s.Status == "downloading" {
			activeDownloadsMu.Unlock()
			return "", errors.New("another model download is already in progress. Only 1 active download is allowed at a time.")
		}
	}

	// Clear completed/failed history to enforce "1 job only" tracking and display
	for k := range activeDownloads {
		delete(activeDownloads, k)
	}

	statusKey := modelStr
	status := &DownloadStatus{
		ModelString: modelStr,
		FileName:    matchFile,
		Status:      "pending",
		Progress:    0,
	}
	activeDownloads[statusKey] = status
	activeDownloadsMu.Unlock()

	// Trigger background download
	go func() {
		err := performDownload(statusKey, downloadURL, targetDir, matchFile, db)
		activeDownloadsMu.Lock()
		if err != nil {
			status.Status = "failed"
			status.Error = err.Error()
		} else {
			status.Status = "completed"
			status.Progress = 100
		}
		activeDownloadsMu.Unlock()
	}()

	return matchFile, nil
}

func performDownload(statusKey string, url string, targetDir string, filename string, db *storage.DB) error {
	activeDownloadsMu.Lock()
	status, exists := activeDownloads[statusKey]
	if exists {
		status.Status = "downloading"
	}
	activeDownloadsMu.Unlock()

	// Create directory if not exists
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return fmt.Errorf("failed to create models directory: %v", err)
	}

	outPath := filepath.Join(targetDir, filename)
	tempPath := outPath + ".download"

	// Check if a partial download already exists
	var localSize int64
	if stat, statErr := os.Stat(tempPath); statErr == nil {
		localSize = stat.Size()
	}

	// Prepare HTTP Request
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create download request: %v", err)
	}

	// Support Hugging Face token authentication for higher API rate limits
	hfToken := os.Getenv("HF_TOKEN")
	if hfToken == "" {
		hfToken = os.Getenv("HF_API_TOKEN")
	}
	hfToken = strings.TrimSpace(hfToken)
	if hfToken != "" {
		req.Header.Set("Authorization", "Bearer "+hfToken)
	}

	// Add Range header if we have partial content on disk
	if localSize > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", localSize))
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to initiate download request: %v", err)
	}

	// Use a closure defer to ensure we always close the active response body
	// even if we re-assign the resp variable during a 416 retry handshake.
	defer func() {
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}
	}()

	// Handle 416 Range Not Satisfiable (e.g. local partial file >= remote file size or file changed)
	// by resetting progress and retrying the download from the beginning.
	if resp.StatusCode == http.StatusRequestedRangeNotSatisfiable {
		resp.Body.Close()
		localSize = 0

		req, err = http.NewRequest("GET", url, nil)
		if err != nil {
			return fmt.Errorf("failed to create retry download request: %v", err)
		}
		if hfToken != "" {
			req.Header.Set("Authorization", "Bearer "+hfToken)
		}

		resp, err = http.DefaultClient.Do(req)
		if err != nil {
			return fmt.Errorf("failed to initiate retry download request: %v", err)
		}
	}

	var out *os.File
	var loaded int64
	var totalSize int64

	if resp.StatusCode == http.StatusPartialContent {
		// Server accepted the range request, open file in append mode
		out, err = os.OpenFile(tempPath, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0644)
		if err != nil {
			return fmt.Errorf("failed to open local file for append: %v", err)
		}
		loaded = localSize
		totalSize = localSize + resp.ContentLength
	} else if resp.StatusCode == http.StatusOK {
		// Range request was ignored or not sent, overwrite/recreate file
		out, err = os.Create(tempPath)
		if err != nil {
			return fmt.Errorf("failed to create local file: %v", err)
		}
		loaded = 0
		totalSize = resp.ContentLength
	} else {
		return fmt.Errorf("download server returned status %d", resp.StatusCode)
	}
	defer out.Close()

	activeDownloadsMu.Lock()
	if exists {
		status.BytesTotal = totalSize
		status.BytesLoaded = loaded
		if totalSize > 0 {
			status.Progress = (float64(loaded) / float64(totalSize)) * 100
		}
	}
	activeDownloadsMu.Unlock()

	buf := make([]byte, 32*1024)
	lastUpdate := time.Now()

	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			_, writeErr := out.Write(buf[:n])
			if writeErr != nil {
				return fmt.Errorf("failed to write to local file: %v", writeErr)
			}
			loaded += int64(n)

			// Throttle progress updates in the UI to prevent high lock contention
			if time.Since(lastUpdate) > 250*time.Millisecond || loaded == totalSize {
				lastUpdate = time.Now()
				activeDownloadsMu.Lock()
				if exists {
					status.BytesLoaded = loaded
					if totalSize > 0 {
						status.Progress = (float64(loaded) / float64(totalSize)) * 100
					}
				}
				activeDownloadsMu.Unlock()
			}
		}
		if err == io.EOF {
			break
		} else if err != nil {
			return fmt.Errorf("error reading download stream: %v", err)
		}
	}

	out.Close()

	// Rename temp file to final GGUF file
	if err := os.Rename(tempPath, outPath); err != nil {
		return fmt.Errorf("failed to finalize model file name: %v", err)
	}

	// Trigger dynamic local scan of the downloaded model to register it instantly
	_, _ = AddManualModel(db, outPath)

	return nil
}
