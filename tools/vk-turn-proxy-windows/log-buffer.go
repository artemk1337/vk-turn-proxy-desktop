// SPDX-License-Identifier: MIT

package main

import (
	"strings"
	"unicode/utf8"
)

const (
	logMaxBytes     = 4 << 20
	defaultLogLines = 1000
	minLogLines     = 100
	maxLogLines     = 10000
)

func normalizeLogLimit(limit int) int {
	if limit <= 0 {
		return defaultLogLines
	}
	return min(maxLogLines, max(minLogLines, limit))
}

type logAppend struct {
	text    string
	suffix  string
	removed int
}

// Retain complete recent lines where possible, including oversized UTF-8 lines.
func planLogAppend(text, line string, limit int) logAppend {
	line = strings.ToValidUTF8(strings.ReplaceAll(line, "\x00", "\ufffd"), "\ufffd")
	line = strings.ReplaceAll(strings.ReplaceAll(line, "\r\n", "\n"), "\r", "")
	suffix := strings.ReplaceAll(line, "\n", "\r\n") + "\r\n"
	return trimLogText(text, suffix, limit)
}

func trimLogText(text, suffix string, limit int) logAppend {
	next := text + suffix
	removed := 0
	for extra := strings.Count(next, "\n") - normalizeLogLimit(limit); extra > 0; extra-- {
		removed += strings.IndexByte(next[removed:], '\n') + 1
	}
	if len(next)-removed > logMaxBytes {
		removed = len(next) - logMaxBytes
		if end := strings.IndexByte(next[removed:], '\n'); end >= 0 && removed+end+1 < len(next) {
			removed += end + 1
		}
		for removed < len(next) && !utf8.RuneStart(next[removed]) {
			removed++
		}
		if removed < len(next) && next[removed] == '\n' {
			removed++
		}
	}
	next = next[removed:]
	suffixRemoved := max(0, removed-len(text))
	return logAppend{text: next, suffix: suffix[suffixRemoved:], removed: min(removed, len(text))}
}

func utf16Length(text string) int {
	length := 0
	for _, r := range text {
		length++
		if r > 0xffff {
			length++
		}
	}
	return length
}
