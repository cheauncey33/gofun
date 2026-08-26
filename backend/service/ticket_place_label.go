package service

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

func placePrefix(tierName string) string {
	trimmed := strings.TrimSpace(tierName)
	var builder strings.Builder
	for _, r := range trimmed {
		if unicode.IsSpace(r) {
			if builder.Len() > 0 {
				builder.WriteRune('-')
			}
			continue
		}
		if r == '-' || unicode.IsLetter(r) || unicode.IsDigit(r) {
			builder.WriteRune(r)
		}
	}
	prefix := strings.Trim(builder.String(), "-")
	if prefix == "" {
		prefix = "区"
	}
	if utf8.RuneCountInString(prefix) > 16 {
		runes := []rune(prefix)
		prefix = string(runes[:16])
	}
	return prefix
}

func formatPlaceLabel(prefix string, seq int) string {
	return fmt.Sprintf("%s-%05d", prefix, seq)
}
