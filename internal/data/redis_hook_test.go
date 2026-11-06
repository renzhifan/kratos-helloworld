package data

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateKeepsShortValues(t *testing.T) {
	const short = "get sf:access_token"
	if got := truncate(short, maxArgsLogLength); got != short {
		t.Fatalf("truncate(%q) = %q, want unchanged", short, got)
	}
}

func TestTruncateCutsLongValuesWithoutBreakingRunes(t *testing.T) {
	long := strings.Repeat("中", maxArgsLogLength+10)
	got := truncate(long, maxArgsLogLength)
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("truncated value does not end with %q: %q", "...", got)
	}
	body := strings.TrimSuffix(got, "...")
	if n := utf8.RuneCountInString(body); n != maxArgsLogLength {
		t.Fatalf("kept %d runes, want %d", n, maxArgsLogLength)
	}
	if !utf8.ValidString(body) {
		t.Fatal("truncated value is not valid UTF-8")
	}
}
