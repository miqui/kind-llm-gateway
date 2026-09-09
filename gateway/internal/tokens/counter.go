// Package tokens provides tiktoken-based token counting for pre-flight
// prompt-size and budget enforcement.
package tokens

import (
	_ "embed"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"sync"

	tiktoken "github.com/pkoukk/tiktoken-go"
)

//go:embed assets/cl100k_base.tiktoken
var cl100kBaseBPE []byte

func init() {
	// Serve the BPE ranks from the vendored asset regardless of the URL
	// tiktoken-go asks for, so no network fetch ever happens at runtime.
	// This is required for llmgw to run inside a network-isolated (e.g.
	// scratch) container.
	tiktoken.SetBpeLoader(embeddedBpeLoader{})
}

// embeddedBpeLoader parses the vendored cl100k_base.tiktoken contents
// directly, bypassing any file/network read entirely.
type embeddedBpeLoader struct{}

func (embeddedBpeLoader) LoadTiktokenBpe(tiktokenBpeFile string) (map[string]int, error) {
	return parseTiktokenBPE(cl100kBaseBPE)
}

// parseTiktokenBPE parses the standard "base64token rank" per-line format
// used by OpenAI's .tiktoken BPE rank files.
func parseTiktokenBPE(contents []byte) (map[string]int, error) {
	ranks := make(map[string]int)
	for _, line := range strings.Split(string(contents), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, " ", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("tokens: malformed bpe rank line: %q", line)
		}
		token, err := base64.StdEncoding.DecodeString(parts[0])
		if err != nil {
			return nil, fmt.Errorf("tokens: decoding bpe token: %w", err)
		}
		rank, err := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err != nil {
			return nil, fmt.Errorf("tokens: parsing bpe rank: %w", err)
		}
		ranks[string(token)] = rank
	}
	return ranks, nil
}

// ChatMessage mirrors the minimal shape of an OpenAI-style chat message
// needed for token counting.
type ChatMessage struct {
	Role    string
	Name    string
	Content string
}

// Per-message and per-reply overhead tokens, following OpenAI's documented
// cl100k_base accounting for gpt-3.5-turbo/gpt-4 style models:
// https://github.com/openai/openai-cookbook (How to count tokens).
const (
	tokensPerMessage = 3
	tokensPerName    = 1
	replyPrimeTokens = 3
)

var (
	encOnce sync.Once
	enc     *tiktoken.Tiktoken
	encErr  error
)

func encoding() (*tiktoken.Tiktoken, error) {
	encOnce.Do(func() {
		enc, encErr = tiktoken.GetEncoding("cl100k_base")
	})
	return enc, encErr
}

// CountTokens returns the number of tokens the cl100k_base tokenizer
// produces for text. It panics only if the bundled encoding cannot be
// loaded, which indicates a packaging bug rather than a runtime condition.
func CountTokens(text string) int {
	e, err := encoding()
	if err != nil {
		panic("tokens: failed to load cl100k_base encoding: " + err.Error())
	}
	return len(e.Encode(text, nil, nil))
}

// CountChatTokens sums per-message overhead plus content tokens for a slice
// of chat messages, following the OpenAI chat-completion token accounting
// convention (constant per-message and per-name overhead, plus a constant
// priming overhead for the assistant's reply).
func CountChatTokens(messages []ChatMessage) int {
	total := replyPrimeTokens
	for _, m := range messages {
		total += tokensPerMessage
		total += CountTokens(m.Role)
		total += CountTokens(m.Content)
		if m.Name != "" {
			total += tokensPerName
			total += CountTokens(m.Name)
		}
	}
	return total
}
