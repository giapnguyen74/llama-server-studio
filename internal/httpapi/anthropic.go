package httpapi

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Anthropic Messages API — request types
// ---------------------------------------------------------------------------

type anthropicMessagesReq struct {
	Model         string         `json:"model"`
	Messages      []anthropicMsg `json:"messages"`
	System        string         `json:"system,omitempty"`
	MaxTokens     int            `json:"max_tokens,omitempty"`
	Temperature   *float64       `json:"temperature,omitempty"`
	TopP          *float64       `json:"top_p,omitempty"`
	Stream        bool           `json:"stream,omitempty"`
	StopSequences []string       `json:"stop_sequences,omitempty"`
}

// anthropicMsg.Content may be a plain string or an array of content blocks.
type anthropicMsg struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"`
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// anthropicContentStr flattens an Anthropic content value (string or block
// array) to a plain string suitable for the OpenAI messages format.
func anthropicContentStr(content interface{}) string {
	switch v := content.(type) {
	case string:
		return v
	case []interface{}:
		var sb strings.Builder
		for _, item := range v {
			if block, ok := item.(map[string]interface{}); ok {
				if block["type"] == "text" {
					if text, ok := block["text"].(string); ok {
						sb.WriteString(text)
					}
				}
			}
		}
		return sb.String()
	}
	return ""
}

func mapFinishReason(openAIReason string) string {
	switch openAIReason {
	case "stop":
		return "end_turn"
	case "length":
		return "max_tokens"
	case "content_filter":
		return "stop_sequence"
	default:
		if openAIReason == "" {
			return "end_turn"
		}
		return "end_turn"
	}
}

func writeAnthropicSSE(w http.ResponseWriter, eventType string, data interface{}) {
	b, _ := json.Marshal(data)
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, string(b))
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func writeAnthropicError(w http.ResponseWriter, code int, errType, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"type": "error",
		"error": map[string]interface{}{
			"type":    errType,
			"message": message,
		},
	})
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

// handleAnthropicListModels returns all profiles in the Anthropic models list
// format (GET /api/v1/models on the gateway port).
func (s *Server) handleAnthropicListModels(w http.ResponseWriter, r *http.Request) {
	profiles := s.db.ListProfiles()
	var models []map[string]interface{}
	for _, p := range profiles {
		created := time.Now().Format(time.RFC3339)
		if p.CreatedAt != "" {
			created = p.CreatedAt
		}
		models = append(models, map[string]interface{}{
			"type":         "model",
			"id":           p.ID,
			"display_name": p.Name,
			"created_at":   created,
		})
	}
	if models == nil {
		models = []map[string]interface{}{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"data":     models,
		"has_more": false,
		"first_id": nil,
		"last_id":  nil,
	})
}

// handleAnthropicMessages is the core Anthropic Messages endpoint
// (POST /api/v1/messages on the gateway port).
// It translates the request to OpenAI format, proxies to the running
// llama-server for the resolved profile, and translates the response back.
func (s *Server) handleAnthropicMessages(w http.ResponseWriter, r *http.Request) {
	// Read body
	body, err := io.ReadAll(io.LimitReader(r.Body, 16*1024*1024))
	if err != nil {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", "Failed to read request body")
		return
	}

	var req anthropicMessagesReq
	if err := json.Unmarshal(body, &req); err != nil {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", "Invalid JSON: "+err.Error())
		return
	}

	// Resolve model / profile
	model := req.Model
	if model == "" {
		if s.cfg.GatewayDefaultModel != "" {
			model = s.cfg.GatewayDefaultModel
		} else {
			writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", "model is required")
			return
		}
	}

	p, ok := s.db.GetProfile(model)
	if !ok {
		writeAnthropicError(w, http.StatusNotFound, "not_found_error", fmt.Sprintf("Model '%s' not found", model))
		return
	}

	upstreamBase, ok := s.proxyRouter.GetUpstreamURL(p)
	if !ok {
		writeAnthropicError(w, http.StatusConflict, "overloaded_error",
			fmt.Sprintf("No running server for model '%s'. Start it from the Server Lifecycle tab.", model))
		return
	}

	// ---------------------------------------------------------------------------
	// Translate: Anthropic → OpenAI
	// ---------------------------------------------------------------------------
	var oaiMessages []map[string]interface{}

	if req.System != "" {
		oaiMessages = append(oaiMessages, map[string]interface{}{
			"role":    "system",
			"content": req.System,
		})
	}
	for _, m := range req.Messages {
		oaiMessages = append(oaiMessages, map[string]interface{}{
			"role":    m.Role,
			"content": anthropicContentStr(m.Content),
		})
	}

	oaiReq := map[string]interface{}{
		"model":    model,
		"messages": oaiMessages,
		"stream":   req.Stream,
	}
	if req.MaxTokens > 0 {
		oaiReq["max_tokens"] = req.MaxTokens
	}
	if req.Temperature != nil {
		oaiReq["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		oaiReq["top_p"] = *req.TopP
	}
	if len(req.StopSequences) > 0 {
		oaiReq["stop"] = req.StopSequences
	}

	oaiBody, _ := json.Marshal(oaiReq)

	// ---------------------------------------------------------------------------
	// Forward to upstream llama-server
	// ---------------------------------------------------------------------------
	upstreamReq, err := http.NewRequestWithContext(
		r.Context(),
		http.MethodPost,
		upstreamBase+"/v1/chat/completions",
		bytes.NewReader(oaiBody),
	)
	if err != nil {
		writeAnthropicError(w, http.StatusInternalServerError, "api_error", "Failed to create upstream request")
		return
	}
	upstreamReq.Header.Set("Content-Type", "application/json")

	// llama-server does not enforce auth; send a placeholder so it won't reject.
	upstreamReq.Header.Set("Authorization", "Bearer llama-server-studio-internal")

	httpClient := &http.Client{} // no global timeout — streaming responses can be arbitrarily long
	resp, err := httpClient.Do(upstreamReq)
	if err != nil {
		writeAnthropicError(w, http.StatusBadGateway, "api_error", "Upstream request failed: "+err.Error())
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(resp.Body)
		writeAnthropicError(w, resp.StatusCode, "api_error",
			fmt.Sprintf("Upstream returned %d: %s", resp.StatusCode, string(errBody)))
		return
	}

	msgID := "msg_" + generateUUID()

	if req.Stream {
		s.streamAnthropicResponse(w, resp.Body, msgID, model)
	} else {
		s.writeAnthropicResponse(w, resp.Body, msgID, model)
	}
}

// ---------------------------------------------------------------------------
// Non-streaming response translation
// ---------------------------------------------------------------------------

func (s *Server) writeAnthropicResponse(w http.ResponseWriter, body io.Reader, msgID, model string) {
	respBytes, err := io.ReadAll(body)
	if err != nil {
		writeAnthropicError(w, http.StatusBadGateway, "api_error", "Failed to read upstream response")
		return
	}

	var oaiResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(respBytes, &oaiResp); err != nil {
		writeAnthropicError(w, http.StatusBadGateway, "api_error", "Failed to parse upstream response")
		return
	}

	text := ""
	stopReason := "end_turn"
	if len(oaiResp.Choices) > 0 {
		text = oaiResp.Choices[0].Message.Content
		stopReason = mapFinishReason(oaiResp.Choices[0].FinishReason)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"id":            msgID,
		"type":          "message",
		"role":          "assistant",
		"content":       []map[string]interface{}{{"type": "text", "text": text}},
		"model":         model,
		"stop_reason":   stopReason,
		"stop_sequence": nil,
		"usage": map[string]int{
			"input_tokens":  oaiResp.Usage.PromptTokens,
			"output_tokens": oaiResp.Usage.CompletionTokens,
		},
	})
}

// ---------------------------------------------------------------------------
// Streaming response translation
// OpenAI SSE chunks → Anthropic SSE event sequence
// ---------------------------------------------------------------------------

func (s *Server) streamAnthropicResponse(w http.ResponseWriter, body io.Reader, msgID, model string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	// 1. message_start
	writeAnthropicSSE(w, "message_start", map[string]interface{}{
		"type": "message_start",
		"message": map[string]interface{}{
			"id":            msgID,
			"type":          "message",
			"role":          "assistant",
			"content":       []interface{}{},
			"model":         model,
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage":         map[string]int{"input_tokens": 0, "output_tokens": 0},
		},
	})

	// 2. content_block_start
	writeAnthropicSSE(w, "content_block_start", map[string]interface{}{
		"type":  "content_block_start",
		"index": 0,
		"content_block": map[string]interface{}{
			"type": "text",
			"text": "",
		},
	})

	// 3. ping
	writeAnthropicSSE(w, "ping", map[string]interface{}{"type": "ping"})

	// 4. Stream OpenAI chunks → content_block_delta events
	type oaiChunk struct {
		Choices []struct {
			Delta struct {
				Content string `json:"content"`
			} `json:"delta"`
			FinishReason *string `json:"finish_reason"`
		} `json:"choices"`
		Usage *struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}

	scanner := bufio.NewScanner(body)
	var finishReason string
	var outputTokens int

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var chunk oaiChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}

		// Capture usage if the upstream includes it (llama-server may emit it
		// in the final chunk).
		if chunk.Usage != nil {
			outputTokens = chunk.Usage.CompletionTokens
		}

		if len(chunk.Choices) == 0 {
			continue
		}

		ch := chunk.Choices[0]
		if ch.FinishReason != nil && *ch.FinishReason != "" {
			finishReason = *ch.FinishReason
		}

		if ch.Delta.Content != "" {
			writeAnthropicSSE(w, "content_block_delta", map[string]interface{}{
				"type":  "content_block_delta",
				"index": 0,
				"delta": map[string]interface{}{
					"type": "text_delta",
					"text": ch.Delta.Content,
				},
			})
		}
	}

	// 5. content_block_stop
	writeAnthropicSSE(w, "content_block_stop", map[string]interface{}{
		"type":  "content_block_stop",
		"index": 0,
	})

	// 6. message_delta (stop_reason + final usage)
	writeAnthropicSSE(w, "message_delta", map[string]interface{}{
		"type": "message_delta",
		"delta": map[string]interface{}{
			"stop_reason":   mapFinishReason(finishReason),
			"stop_sequence": nil,
		},
		"usage": map[string]int{"output_tokens": outputTokens},
	})

	// 7. message_stop
	writeAnthropicSSE(w, "message_stop", map[string]interface{}{"type": "message_stop"})
}
