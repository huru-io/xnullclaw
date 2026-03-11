package config

import (
	"strings"
	"unicode"
)

// SanitizeText strips newlines, carriage returns, and ASCII control characters
// (except space), then truncates to maxLen runes. Safe for embedding in prompts.
func SanitizeText(s string, maxLen int) string {
	s = stripControl(s)
	if r := []rune(s); len(r) > maxLen {
		s = string(r[:maxLen])
	}
	return s
}

// SanitizeName strips control characters and removes anything outside
// [a-zA-Z0-9 _-], then truncates to maxLen runes.
func SanitizeName(s string, maxLen int) string {
	s = stripControl(s)
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == ' ' || r == '_' || r == '-' {
			b.WriteRune(r)
		}
	}
	s = strings.TrimSpace(b.String())
	if r := []rune(s); len(r) > maxLen {
		s = string(r[:maxLen])
	}
	return s
}

// stripControl replaces \n, \r, and other control characters with spaces.
func stripControl(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r == '\n' || r == '\r' || (unicode.IsControl(r) && r != ' ') {
			b.WriteRune(' ')
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}
