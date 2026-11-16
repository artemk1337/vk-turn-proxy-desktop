// SPDX-License-Identifier: MIT

package main

import (
	"strings"
	"testing"
)

func TestRetryTelemetryAndRecovery(t *testing.T) {
	a := testApp(t)
	a.connection = "credentials"
	a.appendLog(`desktop-retry {"error":"486","retryAfterMs":2000}`)
	state := a.GetState().Connection
	if state.RetryError != "486" || state.RetryAt <= 0 || len(a.GetLogs().Lines) != 0 {
		t.Fatalf("retry state = %+v", state)
	}
	a.appendLog("TURN relay allocated")
	a.appendLog(`desktop-retry {"error":"","retryAfterMs":0}`)
	if state = a.GetState().Connection; state.State != "relay" || state.RetryError != "" || state.RetryAt != 0 {
		t.Fatalf("recovery = %+v", state)
	}
	a.connection = "connected"
	a.appendLog(`desktop-retry {"error":"network","retryAfterMs":1000}`)
	if a.GetState().Connection.RetryError != "network" {
		t.Fatal("established connection retry lost")
	}
	a.connection = "disconnecting"
	a.retryError, a.retryAt = "", 0
	a.appendLog(`desktop-retry {"error":"486","retryAfterMs":1000}`)
	if a.GetState().Connection.RetryError != "" {
		t.Fatal("late retry overrides disconnect")
	}
}

func TestRetryTelemetryRejectsMalformedRecords(t *testing.T) {
	a := testApp(t)
	a.connection = "credentials"
	for _, line := range []string{
		"desktop-retry invalid", `desktop-retry {"error":"486","retryAfterMs":-1}`,
		`desktop-retry {"error":"486","retryAfterMs":600001}`, "desktop-retry " + strings.Repeat("x", 2048),
	} {
		a.appendLog(line)
	}
	if state := a.GetState().Connection; state.RetryError != "" || state.RetryAt != 0 {
		t.Fatalf("invalid state = %+v", state)
	}
	a.appendLog(`desktop-retry {"error":"unrecognized","retryAfterMs":1000}`)
	if a.GetState().Connection.RetryError != "connection" {
		t.Fatal("missing fallback")
	}
}

func TestCachedQuotaCooldownTelemetry(t *testing.T) {
	a := testApp(t)
	a.connection = "credentials"
	a.appendLog(`desktop-retry {"error":"cooldown","retryAfterMs":600000}`)
	if state := a.GetState().Connection; state.RetryError != "cooldown" || state.RetryAt == 0 {
		t.Fatalf("cooldown state = %+v", state)
	}
}
