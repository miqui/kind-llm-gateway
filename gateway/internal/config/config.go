// Package config loads llmgw configuration from environment variables.
package config

import (
	"os"
	"strings"
)

// Config holds runtime configuration for llmgw, sourced from environment
// variables with sane defaults for local/dev use.
type Config struct {
	ListenAddr         string
	AdminAddr          string
	SQLitePath         string
	LlamaChatURL       string
	LlamaEmbedURL      string
	RetrievalURL       string
	JaegerOTLPEndpoint string
	// RedactExtraPatterns are additional regex patterns (as raw strings) to
	// apply during log redaction, on top of the compiled-in defaults.
	RedactExtraPatterns []string
}

func getenv(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

// Load reads configuration from the process environment, applying defaults
// for any unset variables.
func Load() Config {
	return Config{
		ListenAddr:          getenv("LISTEN_ADDR", ":8080"),
		AdminAddr:           getenv("ADMIN_ADDR", ":8081"),
		SQLitePath:          getenv("SQLITE_PATH", "/data/llmgw.db"),
		LlamaChatURL:        getenv("LLAMA_CHAT_URL", "http://llama-svc:8080"),
		LlamaEmbedURL:       getenv("LLAMA_EMBED_URL", "http://llama-svc:8081"),
		RetrievalURL:        getenv("RETRIEVAL_URL", "http://retrieval-svc:8080"),
		JaegerOTLPEndpoint:  getenv("JAEGER_OTLP_ENDPOINT", "http://jaeger:4318"),
		RedactExtraPatterns: splitCSV(os.Getenv("REDACT_EXTRA_PATTERNS")),
	}
}

func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
