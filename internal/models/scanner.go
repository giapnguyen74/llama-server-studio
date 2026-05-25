package models

import (
	"crypto/md5"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"llama-server-studio/internal/gguf"
	"llama-server-studio/internal/storage"
)

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
	// Rebuild catalog freshly on every startup or manual scan by clearing previous cached entries
	_ = db.ClearScannedModels()

	fmt.Printf("\n[Scanner] Starting GGUF model files crawl...\n")
	fmt.Printf("[Scanner]   Scan Targets: %s\n", strings.Join(localDirs, ", "))
	if scanHF && len(hfDirs) > 0 {
		fmt.Printf("[Scanner]   HF Cache Targets: %s\n", strings.Join(hfDirs, ", "))
	}

	scannedFiles := make(map[string]bool)
	processedDirs := make(map[string]bool)
	modelsCount := 0

	// Helper to add/update a GGUF file in DB
	processFile := func(path string, source string) {
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
			name = filepath.Base(resolvedPath)
		}
		if quant == "" {
			quant = InferredQuantizationFromName(filepath.Base(resolvedPath))
		}
		if ctxLen == 0 {
			ctxLen = 2048 // Default fallback context size
		}

		// Infer capabilities
		caps = append(caps, "completion") // Standard capability
		if chatTemplate != "" || strings.Contains(strings.ToLower(name), "instruct") || strings.Contains(strings.ToLower(name), "chat") {
			caps = append(caps, "chat")
		}
		if embLen > 0 && (strings.Contains(strings.ToLower(name), "embed") || strings.Contains(strings.ToLower(name), "bge")) {
			caps = append(caps, "embedding")
		}

		repoID := InferredRepoID(resolvedPath)

		// Create record
		m := storage.Model{
			ID:              modelID,
			Path:            path,
			ResolvedPath:    resolvedPath,
			DisplayName:     filepath.Base(resolvedPath),
			Source:          source,
			RepoID:          repoID,
			SizeBytes:       info.Size(),
			ModifiedAt:      info.ModTime().Format(time.RFC3339),
			Architecture:    arch,
			Quantization:    quant,
			ContextLength:   ctxLen,
			EmbeddingLength: embLen,
			BlockCount:      blockCount,
			TokenizerModel:  tokenizer,
			ChatTemplate:    chatTemplate,
			Capabilities:    caps,
			Hidden:          false,
			ScannedAt:       time.Now().Format(time.RFC3339),
		}

		_ = db.SaveModel(m)
		modelsCount++
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

	fmt.Printf("[Scanner] Crawl finished! Discovered and cataloged %d model files.\n\n", modelsCount)
	return nil
}

// walkDirRecursive custom walker traverses directory trees, following directory symlinks up to maxDepth levels.
func walkDirRecursive(path string, source string, depth int, maxDepth int, processedDirs map[string]bool, processFile func(string, string)) {
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
			walkDirRecursive(fullPath, source, depth+1, maxDepth, processedDirs, processFile)
		} else {
			if strings.HasSuffix(strings.ToLower(name), ".gguf") {
				processFile(fullPath, source)
			}
		}
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
		name = filepath.Base(resolvedPath)
	}
	if quant == "" {
		quant = InferredQuantizationFromName(filepath.Base(resolvedPath))
	}
	if ctxLen == 0 {
		ctxLen = 2048
	}

	caps = append(caps, "completion")
	if chatTemplate != "" || strings.Contains(strings.ToLower(name), "instruct") || strings.Contains(strings.ToLower(name), "chat") {
		caps = append(caps, "chat")
	}
	if embLen > 0 && (strings.Contains(strings.ToLower(name), "embed") || strings.Contains(strings.ToLower(name), "bge")) {
		caps = append(caps, "embedding")
	}

	m := storage.Model{
		ID:              modelID,
		Path:            path,
		ResolvedPath:    resolvedPath,
		DisplayName:     filepath.Base(resolvedPath),
		Source:          "manual",
		SizeBytes:       info.Size(),
		ModifiedAt:      info.ModTime().Format(time.RFC3339),
		Architecture:    arch,
		Quantization:    quant,
		ContextLength:   ctxLen,
		EmbeddingLength: embLen,
		BlockCount:      blockCount,
		TokenizerModel:  tokenizer,
		ChatTemplate:    chatTemplate,
		Capabilities:    caps,
		Hidden:          false,
		ScannedAt:       time.Now().Format(time.RFC3339),
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
