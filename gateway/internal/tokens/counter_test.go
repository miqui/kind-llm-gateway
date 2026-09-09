package tokens

import "testing"

func TestCountTokens_KnownStrings(t *testing.T) {
	cases := []struct {
		text string
		want int
	}{
		{"", 0},
		{"hello", 1},
		{"hello world", 2},
		{"Hello, world!", 4},
		// tiktokenizer.vercel.app cl100k_base reference: "The quick brown fox
		// jumps over the lazy dog" -> 9 tokens.
		{"The quick brown fox jumps over the lazy dog", 9},
	}
	for _, c := range cases {
		got := CountTokens(c.text)
		if got != c.want {
			t.Errorf("CountTokens(%q) = %d, want %d", c.text, got, c.want)
		}
	}
}

func TestCountChatTokens(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "system", Content: "You are a helpful assistant."},
		{Role: "user", Content: "Hello, world!"},
	}
	// 3 (reply prime)
	// + msg1: 3 (overhead) + CountTokens("system") + CountTokens("You are a helpful assistant.")
	// + msg2: 3 (overhead) + CountTokens("user") + CountTokens("Hello, world!")
	want := replyPrimeTokens +
		tokensPerMessage + CountTokens("system") + CountTokens("You are a helpful assistant.") +
		tokensPerMessage + CountTokens("user") + CountTokens("Hello, world!")
	got := CountChatTokens(msgs)
	if got != want {
		t.Errorf("CountChatTokens = %d, want %d", got, want)
	}
	if got == 0 {
		t.Fatal("expected non-zero token count")
	}
}

func TestCountChatTokens_Empty(t *testing.T) {
	got := CountChatTokens(nil)
	if got != replyPrimeTokens {
		t.Errorf("CountChatTokens(nil) = %d, want %d", got, replyPrimeTokens)
	}
}

func TestCountChatTokens_WithName(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "user", Name: "alice", Content: "hi"},
	}
	want := replyPrimeTokens + tokensPerMessage + CountTokens("user") + CountTokens("hi") +
		tokensPerName + CountTokens("alice")
	got := CountChatTokens(msgs)
	if got != want {
		t.Errorf("CountChatTokens with name = %d, want %d", got, want)
	}
}
