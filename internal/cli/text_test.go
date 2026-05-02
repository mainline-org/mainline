package cli

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateWithSuffixKeepsUTF8Valid(t *testing.T) {
	input := strings.Repeat("中文", 60)

	got := truncateWithSuffix(input, 100, "...")

	if !utf8.ValidString(got) {
		t.Fatalf("truncated text should remain valid UTF-8: %q", got)
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("expected ASCII suffix, got %q", got)
	}
	if utf8.RuneCountInString(got) != 100 {
		t.Fatalf("expected 100 runes, got %d", utf8.RuneCountInString(got))
	}
	if strings.ContainsRune(got, utf8.RuneError) {
		t.Fatalf("truncated text should not contain replacement runes: %q", got)
	}
}

func TestTruncateWithSuffixLeavesShortChineseUnchanged(t *testing.T) {
	input := strings.Repeat("中文", 40)

	got := truncateWithSuffix(input, 100, "...")

	if got != input {
		t.Fatalf("short text should not be truncated: got %q", got)
	}
}
