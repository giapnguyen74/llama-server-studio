package models

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// RepoFile describes one file in a Hugging Face repository, normalised so the
// caller does not need to know whether the file is stored via LFS or inline.
type RepoFile struct {
	Filename  string `json:"filename"`
	SizeBytes int64  `json:"size"`
	IsLFS     bool   `json:"is_lfs"`
	SHA256    string `json:"sha256,omitempty"`
}

// Hugging Face API error sentinels.  The HTTP layer turns these into the
// status codes documented in docs/hf_support.md (§6).
var (
	ErrRepoNotFound = errors.New("huggingface repository not found")
	ErrRepoGated    = errors.New("huggingface repository requires authentication (set HF_TOKEN)")
	ErrRepoUpstream = errors.New("huggingface API upstream error")
)

const (
	hfBaseURL       = "https://huggingface.co"
	hfMetadataPath  = "/api/models/%s?blobs=true"
	hfResolvePath   = "/%s/resolve/main/%s"
	hfDefaultTimeout = 30 * time.Second
)

// hfClient is a package-level HTTP client used for metadata and HEAD probes.
// The download worker uses its own client with a longer timeout disabled for
// large LFS streaming GETs (see hf_download.go).
var hfClient = &http.Client{Timeout: hfDefaultTimeout}

// FetchRepoFiles returns the file list for a given Hugging Face repo ID.
//
// repoID is the canonical "<owner>/<name>" form, e.g. "unsloth/Qwen3.6-27B-GGUF".
// The function calls the public model metadata API with `?blobs=true` so that
// LFS files report their real size and OID instead of zero/empty placeholders.
// Authentication uses the same HF_TOKEN / HF_API_TOKEN env vars the rest of the
// package recognises.
func FetchRepoFiles(repoID string) ([]RepoFile, error) {
	repoID = strings.TrimSpace(repoID)
	if repoID == "" {
		return nil, errors.New("repo id is required")
	}
	if !validRepoID(repoID) {
		return nil, fmt.Errorf("invalid repo id %q: expected '<owner>/<name>'", repoID)
	}

	url := hfBaseURL + fmt.Sprintf(hfMetadataPath, repoID)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build metadata request: %w", err)
	}
	applyHFAuth(req)

	resp, err := hfClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRepoUpstream, err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		// fall through to decode
	case http.StatusNotFound:
		return nil, ErrRepoNotFound
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, ErrRepoGated
	default:
		// Read a short error preview to help the user
		preview, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("%w: status %d: %s", ErrRepoUpstream, resp.StatusCode, strings.TrimSpace(string(preview)))
	}

	// The HF API response shape we care about.  `?blobs=true` populates
	// the `lfs` sub-object for LFS-tracked files; plain text/config files
	// only carry a top-level `size`.
	var raw struct {
		Siblings []struct {
			Rfilename string `json:"rfilename"`
			Size      int64  `json:"size"`
			LFS       *struct {
				OID  string `json:"oid"`
				Size int64  `json:"size"`
			} `json:"lfs,omitempty"`
		} `json:"siblings"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("failed to decode HF metadata: %w", err)
	}

	out := make([]RepoFile, 0, len(raw.Siblings))
	for _, sib := range raw.Siblings {
		f := RepoFile{
			Filename:  sib.Rfilename,
			SizeBytes: sib.Size,
		}
		if sib.LFS != nil {
			f.IsLFS = true
			f.SHA256 = sib.LFS.OID
			if sib.LFS.Size > 0 {
				f.SizeBytes = sib.LFS.Size
			}
		}
		out = append(out, f)
	}

	return out, nil
}

var (
	hfToken   string
	hfTokenMu sync.RWMutex
)

// SetHFToken sets a global Hugging Face token loaded from config.json.
func SetHFToken(tok string) {
	hfTokenMu.Lock()
	defer hfTokenMu.Unlock()
	hfToken = strings.TrimSpace(tok)
}

// applyHFAuth attaches a Bearer token from HF_TOKEN, HF_API_TOKEN, or hf_token config.
func applyHFAuth(req *http.Request) {
	tok := strings.TrimSpace(os.Getenv("HF_TOKEN"))
	if tok == "" {
		tok = strings.TrimSpace(os.Getenv("HF_API_TOKEN"))
	}
	if tok == "" {
		hfTokenMu.RLock()
		tok = hfToken
		hfTokenMu.RUnlock()
	}
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
}

// hfTokenConfigured reports whether an authorization token is set either in env or config.
func hfTokenConfigured() bool {
	if strings.TrimSpace(os.Getenv("HF_TOKEN")) != "" || strings.TrimSpace(os.Getenv("HF_API_TOKEN")) != "" {
		return true
	}
	hfTokenMu.RLock()
	defer hfTokenMu.RUnlock()
	return hfToken != ""
}

// validRepoID checks the surface form of a repo id.  Accepts either the
// modern "<owner>/<name>" form or a canonical single-segment name (like
// "gpt2" or "bert-base-uncased") — both are valid HF repo identifiers.
// This is a defensive surface check; the canonical validation is whatever
// the HF server returns.
func validRepoID(s string) bool {
	parts := strings.Split(s, "/")
	if len(parts) < 1 || len(parts) > 2 {
		return false
	}
	for _, p := range parts {
		if p == "" {
			return false
		}
		for _, r := range p {
			ok := (r >= 'a' && r <= 'z') ||
				(r >= 'A' && r <= 'Z') ||
				(r >= '0' && r <= '9') ||
				r == '-' || r == '_' || r == '.'
			if !ok {
				return false
			}
		}
	}
	return true
}

// resolveURL returns the canonical download URL for a file inside a repo on
// the `main` revision.  The actual byte source may be a CDN redirect — see
// hf_download.go for the HEAD-first dance that pins the final URL.
func resolveURL(repoID, filename string) string {
	return hfBaseURL + fmt.Sprintf(hfResolvePath, repoID, filename)
}

// FetchRepoReadme downloads the README.md content from the repo if it exists.
func FetchRepoReadme(repoID string) (string, error) {
	repoID = strings.TrimSpace(repoID)
	if repoID == "" {
		return "", errors.New("repo id is required")
	}
	if !validRepoID(repoID) {
		return "", fmt.Errorf("invalid repo id %q", repoID)
	}

	url := resolveURL(repoID, "README.md")
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("failed to build request: %w", err)
	}
	applyHFAuth(req)

	resp, err := hfClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to fetch README: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return "No README.md found in this repository.", nil
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status %d", resp.StatusCode)
	}

	// Limit to 256KB to avoid massive reads stalling memory
	data, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

