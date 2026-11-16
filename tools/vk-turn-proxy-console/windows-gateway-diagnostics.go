// SPDX-License-Identifier: MIT

package main

import (
	"strings"
	"sync"
	"time"
)

var windowsGatewayQueryDiagnostics windowsGatewayDiagnostic

type windowsGatewayDiagnostic struct {
	mu    sync.Mutex
	issue string
	at    time.Time
}

// message reports changes immediately and repeats a persistent failure once a minute.
func (d *windowsGatewayDiagnostic) message(issue string, now time.Time) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if issue == "" {
		if d.issue == "" {
			return ""
		}
		d.issue, d.at = "", time.Time{}
		return "network: Windows default gateway detection recovered"
	}
	if issue == d.issue && now.Sub(d.at) < time.Minute {
		return ""
	}
	d.issue, d.at = issue, now
	return "network: Windows default gateway detection failed: " + issue
}

func windowsRouteDiagnosticText(text string) string {
	text = strings.TrimSpace(strings.ToValidUTF8(text, "�"))
	runes := []rune(text)
	if len(runes) > 1024 {
		return string(runes[:1024]) + " [truncated]"
	}
	return text
}
