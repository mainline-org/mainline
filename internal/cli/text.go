package cli

import "unicode/utf8"

func truncate(s string, n int) string {
	return truncateWithSuffix(s, n, "…")
}

func truncateWithSuffix(s string, n int, suffix string) string {
	if n <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= n {
		return s
	}

	suffixRunes := []rune(suffix)
	if len(suffixRunes) >= n {
		return string(suffixRunes[:n])
	}

	runes := []rune(s)
	return string(runes[:n-len(suffixRunes)]) + suffix
}
