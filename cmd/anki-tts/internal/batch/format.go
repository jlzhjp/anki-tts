package batch

import "strings"

func compactPreview(value string, limit int) string {
	return truncate(strings.Join(strings.Fields(value), " "), limit)
}

func truncate(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	if limit <= 3 {
		return string(runes[:limit])
	}
	return string(runes[:limit-3]) + "..."
}

func red(value string) string { return "\x1b[1;31m" + value + "\x1b[0m" }
