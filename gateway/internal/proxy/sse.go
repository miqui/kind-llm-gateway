package proxy

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/miqui/kind-llm-gateway/gateway/internal/tokens"
)

// proxyStream relays an SSE upstream response to the client event-by-event,
// flushing after each event, and commits actual usage (parsed from the
// final chunk if present, else estimated from accumulated delta text) once
// the stream ends.
func (h *Handler) proxyStream(w http.ResponseWriter, resp *http.Response, tenantID string, estimated int64, promptTokens int, model, route string, reqBody []byte) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		// Fall back to a non-stream relay if flushing isn't supported
		// (e.g. some test recorders); still commit/refund correctly.
		h.relayNonStream(w, resp, tenantID, estimated, promptTokens, model, route, reqBody)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	if resp.StatusCode >= 500 {
		body, _ := io.ReadAll(resp.Body)
		h.Quota.Refund(tenantID, estimated)
		relayUpstreamHeaders(w, resp)
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(body)
		flusher.Flush()
		return
	}

	w.WriteHeader(resp.StatusCode)

	reader := bufio.NewReader(resp.Body)
	var deltaText strings.Builder
	var finalUsage *usage
	var respBuf bytes.Buffer

	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			respBuf.WriteString(line)
			_, _ = w.Write([]byte(line))
			flusher.Flush()

			if u := parseSSEUsage(line); u != nil {
				finalUsage = u
			}
			deltaText.WriteString(extractSSEDeltaText(line))
		}
		if err != nil {
			break
		}
	}

	var actual int64
	if finalUsage != nil && finalUsage.TotalTokens > 0 {
		actual = int64(finalUsage.TotalTokens)
	} else {
		completionTokens := tokens.CountTokens(deltaText.String())
		actual = int64(promptTokens + completionTokens)
	}
	_ = h.Quota.Commit(tenantID, actual)

	h.logRequest(tenantID, model, route, promptTokens, int(actual-int64(promptTokens)), resp.StatusCode, reqBody, respBuf.Bytes())
}

// parseSSEUsage attempts to parse a "usage" field out of a single SSE
// "data: {...}" line, returning nil if the line isn't JSON or has no usage.
func parseSSEUsage(line string) *usage {
	data, ok := sseData(line)
	if !ok {
		return nil
	}
	var u struct {
		Usage *usage `json:"usage"`
	}
	if err := json.Unmarshal([]byte(data), &u); err != nil {
		return nil
	}
	return u.Usage
}

// extractSSEDeltaText extracts any OpenAI-style
// choices[0].delta.content text from an SSE data line, for token
// estimation when no usage is present in the stream.
func extractSSEDeltaText(line string) string {
	data, ok := sseData(line)
	if !ok {
		return ""
	}
	var chunk struct {
		Choices []struct {
			Delta struct {
				Content string `json:"content"`
			} `json:"delta"`
		} `json:"choices"`
	}
	if err := json.Unmarshal([]byte(data), &chunk); err != nil {
		return ""
	}
	var sb strings.Builder
	for _, c := range chunk.Choices {
		sb.WriteString(c.Delta.Content)
	}
	return sb.String()
}

func sseData(line string) (string, bool) {
	trimmed := strings.TrimRight(line, "\r\n")
	if !strings.HasPrefix(trimmed, "data:") {
		return "", false
	}
	data := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
	if data == "" || data == "[DONE]" {
		return "", false
	}
	return data, true
}
