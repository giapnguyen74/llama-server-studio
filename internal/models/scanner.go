package models

import (
	"crypto/md5"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"llama-server-studio/internal/gguf"
	"llama-server-studio/internal/storage"
)

var mmprojPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)^mmproj-.*\.gguf$`),
	regexp.MustCompile(`(?i)^mmproj\.gguf$`),
	regexp.MustCompile(`(?i).*[-_]mmproj([-_.].*)?\.gguf$`),
}

func IsMMProj(filename string) bool {
	for _, p := range mmprojPatterns {
		if p.MatchString(filename) {
			return true
		}
	}
	return false
}

var (
	scanMu     sync.Mutex
	isScanning bool
)

func IsScanning() bool {
	scanMu.Lock()
	defer scanMu.Unlock()
	return isScanning
}


// Map GGUF general.file_type value to human-readable string.
var fileTypeMap = map[uint32]string{
	0:  "F32",
	1:  "F16",
	2:  "Q4_0",
	3:  "Q4_1",
	7:  "Q8_0",
	8:  "Q5_0",
	9:  "Q5_1",
	10: "Q2_K",
	11: "Q3_K_S",
	12: "Q3_K_M",
	13: "Q3_K_L",
	14: "Q4_K_S",
	15: "Q4_K_M",
	16: "Q5_K_S",
	17: "Q5_K_M",
	18: "Q6_K",
	19: "IQ2_XXS",
	20: "IQ2_XS",
	21: "IQ3_XXS",
	22: "IQ1_S",
	23: "IQ2_M",
	24: "IQ3_S",
	25: "IQ2_S",
	26: "IQ1_M",
	27: "BF16",
}

// InferredQuantizationFromName parses the filename to extract quantization strings if metadata is absent.
func InferredQuantizationFromName(filename string) string {
	filename = strings.ToUpper(filename)
	quants := []string{
		"Q2_K_S", "Q2_K", "Q3_K_S", "Q3_K_M", "Q3_K_L", "Q3_K",
		"Q4_K_S", "Q4_K_M", "Q4_K", "Q4_0", "Q4_1",
		"Q5_K_S", "Q5_K_M", "Q5_K", "Q5_0", "Q5_1",
		"Q6_K", "Q8_0", "F16", "F32", "BF16",
		"IQ1_S", "IQ1_M", "IQ2_XXS", "IQ2_XS", "IQ2_S", "IQ2_M", "IQ3_XXS", "IQ3_S",
	}
	for _, q := range quants {
		if strings.Contains(filename, q) {
			return q
		}
	}
	return "Unknown"
}

// InferredRepoID extracts the Hugging Face repo ID from the path.
func InferredRepoID(path string) string {
	// Standard HF cache path: .../models--<author>--<model>/snapshots/...
	parts := strings.Split(path, string(filepath.Separator))
	for _, part := range parts {
		if strings.HasPrefix(part, "models--") {
			subParts := strings.Split(part[8:], "--")
			if len(subParts) >= 2 {
				return subParts[0] + "/" + strings.Join(subParts[1:], "-")
			} else if len(subParts) == 1 {
				return subParts[0]
			}
		}
	}
	return ""
}

// ScanDirectories recursively searches for `.gguf` files in given folders and updates the DB.
func ScanDirectories(db *storage.DB, localDirs []string, scanHF bool, hfDirs []string) error {
	scanMu.Lock()
	if isScanning {
		scanMu.Unlock()
		return fmt.Errorf("scan already in progress")
	}
	isScanning = true
	scanMu.Unlock()

	defer func() {
		scanMu.Lock()
		isScanning = false
		scanMu.Unlock()
	}()

	fmt.Printf("\n[Scanner] Starting GGUF model files crawl...\n")
	fmt.Printf("[Scanner]   Scan Targets: %s\n", strings.Join(localDirs, ", "))
	if scanHF && len(hfDirs) > 0 {
		fmt.Printf("[Scanner]   HF Cache Targets: %s\n", strings.Join(hfDirs, ", "))
	}

	scannedFiles := make(map[string]bool)
	processedDirs := make(map[string]bool)
	var discoveredModels []storage.Model

	// Helper to add/update a GGUF file in DB with paired mmprojs
	processFile := func(path string, source string, mmprojs []string) {
		resolvedPath, err := filepath.EvalSymlinks(path)
		if err != nil {
			resolvedPath = path // Fallback to path if symlink can't be resolved
		}

		// Avoid double processing the same physical file
		if scannedFiles[resolvedPath] {
			return
		}
		scannedFiles[resolvedPath] = true

		info, err := os.Stat(resolvedPath)
		if err != nil || info.IsDir() || info.Size() == 0 {
			return
		}

		sizeGB := float64(info.Size()) / (1024 * 1024 * 1024)
		fmt.Printf("[Scanner] Found GGUF file: %s (%.2f GB)\n", filepath.Base(resolvedPath), sizeGB)

		// Generate stable ID from path hash
		h := md5.New()
		h.Write([]byte(resolvedPath))
		modelID := fmt.Sprintf("%x", h.Sum(nil))

		// Parse GGUF metadata
		metadata, err := gguf.ReadMetadata(resolvedPath)
		var arch, name, quant, tokenizer, chatTemplate string
		var ctxLen, embLen, blockCount int
		var caps []string
		
		if err == nil {
			// Extract known fields
			if a, ok := metadata["general.architecture"].(string); ok {
				arch = a
			}
			if n, ok := metadata["general.name"].(string); ok {
				name = n
			}
			if t, ok := metadata["tokenizer.ggml.model"].(string); ok {
				tokenizer = t
			}
			if ct, ok := metadata["tokenizer.chat_template"].(string); ok {
				chatTemplate = ct
			}

			// Integers
			if cl, ok := metadata["llama.context_length"]; ok {
				ctxLen = toInt(cl)
			} else if cl, ok := metadata[arch+".context_length"]; ok {
				ctxLen = toInt(cl)
			}
			if el, ok := metadata["llama.embedding_length"]; ok {
				embLen = toInt(el)
			} else if el, ok := metadata[arch+".embedding_length"]; ok {
				embLen = toInt(el)
			}
			if bc, ok := metadata["llama.block_count"]; ok {
				blockCount = toInt(bc)
			} else if bc, ok := metadata[arch+".block_count"]; ok {
				blockCount = toInt(bc)
			}

			// Quantization
			if ft, ok := metadata["general.file_type"].(uint32); ok {
				quant = fileTypeMap[ft]
			}

			fmt.Printf("[Scanner]   └─ GGUF Header parsed successfully (Arch: %s, Quant: %s, Context: %d)\n", arch, quant, ctxLen)
		} else {
			fmt.Printf("[Scanner]   └─ [Warning] GGUF metadata parsing failed: %v. Using fallbacks.\n", err)
		}

		// Fallbacks
		if name == "" {
			name = filepath.Base(path)
		}
		if quant == "" {
			quant = InferredQuantizationFromName(filepath.Base(path))
		}
		if ctxLen == 0 {
			ctxLen = 2048 // Default fallback context size
		}

		// Resolve and gather mmproj candidates
		var mmprojCandidates []string
		var mmprojSizesBytes []int64
		mmprojDedup := make(map[string]bool)

		for _, mmprojPath := range mmprojs {
			resolvedMMProj, err := filepath.EvalSymlinks(mmprojPath)
			if err != nil {
				resolvedMMProj = mmprojPath
			}
			if mmprojDedup[resolvedMMProj] {
				continue
			}
			mmprojDedup[resolvedMMProj] = true

			mmInfo, err := os.Stat(resolvedMMProj)
			if err != nil {
				continue
			}
			mmprojCandidates = append(mmprojCandidates, resolvedMMProj)
			mmprojSizesBytes = append(mmprojSizesBytes, mmInfo.Size())
		}

		// Infer capabilities
		caps = append(caps, "completion") // Standard capability
		if chatTemplate != "" || strings.Contains(strings.ToLower(name), "instruct") || strings.Contains(strings.ToLower(name), "chat") {
			caps = append(caps, "chat")
		}
		if embLen > 0 && (strings.Contains(strings.ToLower(name), "embed") || strings.Contains(strings.ToLower(name), "bge")) {
			caps = append(caps, "embedding")
		}
		if len(mmprojCandidates) > 0 {
			caps = append(caps, "vision")
		}

		repoID := InferredRepoID(resolvedPath)

		// Create record
		m := storage.Model{
			ID:               modelID,
			Path:             path,
			ResolvedPath:     resolvedPath,
			DisplayName:      filepath.Base(path),
			Source:           source,
			RepoID:           repoID,
			SizeBytes:        info.Size(),
			ModifiedAt:       info.ModTime().Format(time.RFC3339),
			Architecture:     arch,
			Quantization:     quant,
			ContextLength:    ctxLen,
			EmbeddingLength:  embLen,
			BlockCount:       blockCount,
			TokenizerModel:   tokenizer,
			ChatTemplate:     chatTemplate,
			Capabilities:     caps,
			Hidden:           false,
			ScannedAt:        time.Now().Format(time.RFC3339),
			MMProjCandidates: mmprojCandidates,
			MMProjSizesBytes: mmprojSizesBytes,
		}

		discoveredModels = append(discoveredModels, m)
	}

	// 1. Scan user local directories
	for _, dir := range localDirs {
		walkDirRecursive(dir, "local_dir", 1, 8, processedDirs, processFile)
	}

	// 2. Scan Hugging Face cache if active
	if scanHF {
		for _, dir := range hfDirs {
			walkDirRecursive(dir, "huggingface_cache", 1, 8, processedDirs, processFile)
		}
	}

	if err := db.ReplaceScannedModels(discoveredModels); err != nil {
		return fmt.Errorf("failed to save scanned models: %w", err)
	}

	fmt.Printf("[Scanner] Crawl finished! Discovered and cataloged %d model files.\n\n", len(discoveredModels))
	return nil
}

// walkDirRecursive custom walker traverses directory trees, following directory symlinks up to maxDepth levels.
func walkDirRecursive(path string, source string, depth int, maxDepth int, processedDirs map[string]bool, processFile func(string, string, []string)) {
	if depth > maxDepth {
		return
	}

	resolvedPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return
	}

	// Avoid directory cycles / double traversals
	if processedDirs[resolvedPath] {
		return
	}
	processedDirs[resolvedPath] = true

	entries, err := os.ReadDir(resolvedPath)
	if err != nil {
		return
	}

	var localModels []string
	var localMMProjs []string
	var subDirs []string

	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue // Skip hidden directories
		}

		fullPath := filepath.Join(resolvedPath, name)
		
		isDir := entry.IsDir()
		if !isDir && (entry.Type()&os.ModeSymlink != 0) {
			// Probe if directory symlink
			if info, err := os.Stat(fullPath); err == nil {
				isDir = info.IsDir()
			}
		}

		if isDir {
			subDirs = append(subDirs, fullPath)
		} else {
			if strings.HasSuffix(strings.ToLower(name), ".gguf") {
				if IsMMProj(name) {
					localMMProjs = append(localMMProjs, fullPath)
				} else {
					localModels = append(localModels, fullPath)
				}
			}
		}
	}

	// Process base models in the current directory with local mmprojs paired
	for _, mPath := range localModels {
		processFile(mPath, source, localMMProjs)
	}

	// Recurse into subdirectories
	for _, subDir := range subDirs {
		walkDirRecursive(subDir, source, depth+1, maxDepth, processedDirs, processFile)
	}
}

// AddManualModel loads a model from a single direct file path.
func AddManualModel(db *storage.DB, path string) (storage.Model, error) {
	resolvedPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		resolvedPath = path
	}

	info, err := os.Stat(resolvedPath)
	if err != nil {
		return storage.Model{}, err
	}
	if info.IsDir() {
		return storage.Model{}, fmt.Errorf("path is a directory: %s", path)
	}

	h := md5.New()
	h.Write([]byte(resolvedPath))
	modelID := fmt.Sprintf("%x", h.Sum(nil))

	// MMProj filename heuristic check
	if IsMMProj(filepath.Base(resolvedPath)) {
		// Just-downloaded mmproj file. Find any base models in the DB that share the same directory,
		// and add this mmproj to their candidates if not already present.
		dir := filepath.Dir(resolvedPath)
		for _, mRecord := range db.ListModels() {
			modelDir := filepath.Dir(mRecord.ResolvedPath)
			if modelDir == dir {
				alreadyPresent := false
				for _, cand := range mRecord.MMProjCandidates {
					if cand == resolvedPath {
						alreadyPresent = true
						break
					}
				}
				if !alreadyPresent {
					mRecord.MMProjCandidates = append(mRecord.MMProjCandidates, resolvedPath)
					mRecord.MMProjSizesBytes = append(mRecord.MMProjSizesBytes, info.Size())
					
					hasVision := false
					for _, c := range mRecord.Capabilities {
						if c == "vision" {
							hasVision = true
							break
						}
					}
					if !hasVision {
						mRecord.Capabilities = append(mRecord.Capabilities, "vision")
					}
					_ = db.SaveModel(mRecord)
				}
			}
		}
		// Return mmproj representation without saving it in the main catalog database
		return storage.Model{
			ID:           modelID,
			Path:         path,
			ResolvedPath: resolvedPath,
			DisplayName:  filepath.Base(path),
			Source:       "manual",
			SizeBytes:    info.Size(),
			ScannedAt:    time.Now().Format(time.RFC3339),
		}, nil
	}

	metadata, err := gguf.ReadMetadata(resolvedPath)
	var arch, name, quant, tokenizer, chatTemplate string
	var ctxLen, embLen, blockCount int
	var caps []string

	if err == nil {
		if a, ok := metadata["general.architecture"].(string); ok {
			arch = a
		}
		if n, ok := metadata["general.name"].(string); ok {
			name = n
		}
		if t, ok := metadata["tokenizer.ggml.model"].(string); ok {
			tokenizer = t
		}
		if ct, ok := metadata["tokenizer.chat_template"].(string); ok {
			chatTemplate = ct
		}

		if cl, ok := metadata["llama.context_length"]; ok {
			ctxLen = toInt(cl)
		} else if cl, ok := metadata[arch+".context_length"]; ok {
			ctxLen = toInt(cl)
		}
		if el, ok := metadata["llama.embedding_length"]; ok {
			embLen = toInt(el)
		} else if el, ok := metadata[arch+".embedding_length"]; ok {
			embLen = toInt(el)
		}
		if bc, ok := metadata["llama.block_count"]; ok {
			blockCount = toInt(bc)
		} else if bc, ok := metadata[arch+".block_count"]; ok {
			blockCount = toInt(bc)
		}

		if ft, ok := metadata["general.file_type"].(uint32); ok {
			quant = fileTypeMap[ft]
		}
	}

	if name == "" {
		name = filepath.Base(path)
	}
	if quant == "" {
		quant = InferredQuantizationFromName(filepath.Base(path))
	}
	if ctxLen == 0 {
		ctxLen = 2048
	}

	// Look for sibling mmproj files
	dir := filepath.Dir(resolvedPath)
	entries, err := os.ReadDir(dir)
	var mmprojCandidates []string
	var mmprojSizesBytes []int64

	if err == nil {
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasSuffix(strings.ToLower(entry.Name()), ".gguf") && IsMMProj(entry.Name()) {
				mPath := filepath.Join(dir, entry.Name())
				resolvedMPath, err := filepath.EvalSymlinks(mPath)
				if err != nil {
					resolvedMPath = mPath
				}
				mmInfo, err := os.Stat(resolvedMPath)
				if err == nil {
					mmprojCandidates = append(mmprojCandidates, resolvedMPath)
					mmprojSizesBytes = append(mmprojSizesBytes, mmInfo.Size())
				}
			}
		}
	}

	caps = append(caps, "completion")
	if chatTemplate != "" || strings.Contains(strings.ToLower(name), "instruct") || strings.Contains(strings.ToLower(name), "chat") {
		caps = append(caps, "chat")
	}
	if embLen > 0 && (strings.Contains(strings.ToLower(name), "embed") || strings.Contains(strings.ToLower(name), "bge")) {
		caps = append(caps, "embedding")
	}
	if len(mmprojCandidates) > 0 {
		caps = append(caps, "vision")
	}

	m := storage.Model{
		ID:               modelID,
		Path:             path,
		ResolvedPath:     resolvedPath,
		DisplayName:      filepath.Base(path),
		Source:           "manual",
		SizeBytes:        info.Size(),
		ModifiedAt:       info.ModTime().Format(time.RFC3339),
		Architecture:     arch,
		Quantization:     quant,
		ContextLength:    ctxLen,
		EmbeddingLength:  embLen,
		BlockCount:       blockCount,
		TokenizerModel:   tokenizer,
		ChatTemplate:     chatTemplate,
		Capabilities:     caps,
		Hidden:           false,
		ScannedAt:        time.Now().Format(time.RFC3339),
		MMProjCandidates: mmprojCandidates,
		MMProjSizesBytes: mmprojSizesBytes,
	}

	err = db.SaveModel(m)
	return m, err
}

func toInt(v interface{}) int {
	switch val := v.(type) {
	case uint8:
		return int(val)
	case int8:
		return int(val)
	case uint16:
		return int(val)
	case int16:
		return int(val)
	case uint32:
		return int(val)
	case int32:
		return int(val)
	case uint64:
		return int(val)
	case int64:
		return int(val)
	case int:
		return val
	}
	return 0
}
