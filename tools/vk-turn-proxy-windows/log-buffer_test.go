// SPDX-License-Identifier: MIT

package main

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestLogAppendPreservesOrderAndNormalizesNativeText(t *testing.T) {
	plan := planLogAppend("first\r\n", "second\nthird\x00", defaultLogLines)
	if plan.text != "first\r\nsecond\r\nthird\ufffd\r\n" || plan.removed != 0 || plan.suffix != "second\r\nthird\ufffd\r\n" {
		t.Fatalf("unexpected append plan: %#v", plan)
	}
}

func TestLogAppendTrimsWholeOldLines(t *testing.T) {
	text := strings.Repeat("old entry\r\n", logMaxBytes/11)
	plan := planLogAppend(text, strings.Repeat("new", 100), maxLogLines)
	if len(plan.text) > logMaxBytes || !strings.HasPrefix(plan.text, "old entry\r\n") || !strings.HasSuffix(plan.text, strings.Repeat("new", 100)+"\r\n") {
		t.Fatal("log retention lost line boundaries or latest entry")
	}
	if text[plan.removed:]+plan.suffix != plan.text {
		t.Fatal("incremental update differs from retained text")
	}
}

func TestLogAppendBoundsOversizedUnicodeEntry(t *testing.T) {
	old := "old\r\n"
	plan := planLogAppend(old, strings.Repeat("😀", logMaxBytes), defaultLogLines)
	if len(plan.text) > logMaxBytes || !utf8.ValidString(plan.text) || !strings.HasSuffix(plan.text, "😀\r\n") {
		t.Fatal("oversized log line broke UTF-8 or the byte limit")
	}
	if plan.removed != len(old) || plan.suffix != plan.text {
		t.Fatal("oversized entry retained stale text")
	}
}

func TestLogSelectionOffsetsUseUTF16Units(t *testing.T) {
	if utf16Length("a😀я\r\n") != 6 {
		t.Fatal("Windows edit offsets must count surrogate pairs")
	}
}

func TestLogRetainsConfiguredNumberOfLines(t *testing.T) {
	text := ""
	for batch := 0; batch < 20; batch++ {
		plan := planLogAppend(text, strings.Repeat("entry\n", 99)+"last", 1000)
		text = plan.text
		if strings.Count(text, "\n") > 1000 {
			t.Fatal("log exceeded line limit")
		}
	}
	if strings.Count(text, "\n") != 1000 {
		t.Fatal("log did not retain recent lines")
	}
	shrunk := trimLogText(text, "", 100)
	if strings.Count(shrunk.text, "\n") != 100 || text[shrunk.removed:] != shrunk.text {
		t.Fatal("shrinking log lost incremental retention")
	}
}
