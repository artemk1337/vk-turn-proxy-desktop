// SPDX-License-Identifier: MIT

package main

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestWindowsGatewayDiagnosticReasons(t *testing.T) {
	for _, test := range []struct {
		output string
		reason string
	}{
		{"", "no IPv4 default route"},
		{"null", "no IPv4 default route"},
		{"not JSON", "cannot parse Get-NetRoute JSON"},
		{`{"NextHop":"bad","InterfaceAlias":"Wi-Fi"}`, "invalid NextHop"},
		{`{"NextHop":"192.168.1.1","InterfaceAlias":""}`, "empty InterfaceAlias"},
	} {
		if _, err := parseWindowsDefaultRouteDetail(test.output); err == nil || !strings.Contains(err.Error(), test.reason) {
			t.Fatalf("output %q: reason %v, want %q", test.output, err, test.reason)
		}
	}
	gw, err := parseWindowsDefaultRouteDetail(`{"NextHop":"192.168.1.1","InterfaceAlias":"Беспроводная сеть"}`)
	if err != nil || gw.IP != "192.168.1.1" || gw.Iface != "Беспроводная сеть" {
		t.Fatalf("gateway %+v, error %v", gw, err)
	}
}

func TestWindowsGatewayDiagnosticRateLimitAndRecovery(t *testing.T) {
	var diagnostic windowsGatewayDiagnostic
	now := time.Now()
	if diagnostic.message("", now) != "" {
		t.Fatal("initial success produced a recovery message")
	}
	if diagnostic.message("query failed", now) == "" {
		t.Fatal("first failure was suppressed")
	}
	if diagnostic.message("query failed", now.Add(59*time.Second)) != "" {
		t.Fatal("persistent failure repeated before one minute")
	}
	if diagnostic.message("query failed", now.Add(time.Minute)) == "" {
		t.Fatal("persistent failure was not repeated after one minute")
	}
	if diagnostic.message("invalid JSON", now.Add(time.Minute)) == "" {
		t.Fatal("changed failure reason was suppressed")
	}
	if !strings.Contains(diagnostic.message("", now.Add(time.Minute)), "recovered") {
		t.Fatal("recovery was not reported")
	}
	if diagnostic.message("", now.Add(2*time.Minute)) != "" {
		t.Fatal("recovery was reported twice")
	}
}

func TestWindowsRouteDiagnosticTextIsBoundedUTF8(t *testing.T) {
	text := windowsRouteDiagnosticText(strings.Repeat("я", 2000))
	if !utf8.ValidString(text) || len([]rune(text)) != 1024+len(" [truncated]") || !strings.HasSuffix(text, " [truncated]") {
		t.Fatalf("invalid diagnostic truncation: %d bytes", len(text))
	}
}
