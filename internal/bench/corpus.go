package bench

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type Workload struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Prompt      string  `json:"prompt"`
	MaxTokens   int     `json:"max_tokens"`
	Temperature float64 `json:"temperature"`
}

var BuiltInWorkloads = []Workload{
	{
		ID:          "short_chat",
		Name:        "Short Chat (Q&A)",
		Prompt:      "Translate the following sentence into French, German, and Japanese: 'Artificial intelligence is changing the way we interact with technology.'",
		MaxTokens:   64,
		Temperature: 0.2,
	},
	{
		ID:          "medium_chat",
		Name:        "Medium Chat (Conversation Turn)",
		Prompt:      "Explain the key differences between SQL and NoSQL databases. Provide a comparison table including scaling, consistency guarantees, and schema flexibility, then summarize when to use which in three concise paragraphs.",
		MaxTokens:   256,
		Temperature: 0.7,
	},
	{
		ID:          "creative_long",
		Name:        "Creative Long Writing",
		Prompt:      "Write a science fiction short story about an astronaut who discovers an ancient, solar-powered lighthouse floating in the asteroid belt. The story should be atmospheric, descriptive, and focus on the sense of isolation and wonder.",
		MaxTokens:   512,
		Temperature: 0.8,
	},
	{
		ID:          "long_context",
		Name:        "Long Context Ingestion",
		Prompt:      "Analyze the importance of attention mechanisms in transformer networks. How does self-attention differ from cross-attention? Detail the mathematical queries, keys, and values matrices, and discuss why scaling factors are necessary.",
		MaxTokens:   256,
		Temperature: 0.0,
	},
}

// GetWorkloads returns built-in workloads and merges any custom JSON files loaded from the user's data directory.
func GetWorkloads(dataDir string) []Workload {
	workloads := append([]Workload{}, BuiltInWorkloads...)

	if dataDir == "" {
		return workloads
	}

	corpusDir := filepath.Join(dataDir, "bench", "corpus")
	files, err := os.ReadDir(corpusDir)
	if err != nil {
		return workloads
	}

	for _, file := range files {
		if filepath.Ext(file.Name()) == ".json" {
			path := filepath.Join(corpusDir, file.Name())
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			var wl Workload
			if err := json.Unmarshal(data, &wl); err == nil && wl.ID != "" {
				workloads = append(workloads, wl)
			}
		}
	}

	return workloads
}
