// SPDX-License-Identifier: MIT

package main

import (
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestParseTraffic(t *testing.T) {
	for _, test := range []struct {
		line string
		ok   bool
	}{
		{"desktop-traffic 123 456", true},
		{"desktop-traffic 0 9223372036854775807", true},
		{"desktop-traffic 1", false},
		{"desktop-traffic 1 2 3", false},
		{"desktop-traffic -1 2", false},
		{"desktop-traffic 1 -2", false},
		{"desktop-traffic 9223372036854775808 1", false},
		{"desktop-traffic 1 18446744073709551616", false},
		{"desktop-traffic 1.5 2", false},
		{"desktop-traffic abc 2", false},
		{"12:00:00 desktop-traffic 1 2", false},
		{"desktop-traffic-other 1 2", false},
	} {
		t.Run(test.line, func(t *testing.T) {
			_, _, ok := parseTraffic(test.line)
			if ok != test.ok {
				t.Fatalf("accepted = %v, want %v", ok, test.ok)
			}
		})
	}
	if tx, rx, ok := parseTraffic("desktop-traffic 123 456"); !ok || tx != 123 || rx != 456 {
		t.Fatalf("counters = %d, %d, %v", tx, rx, ok)
	}
}

func TestTrafficRateUsesElapsedTimeAndExpires(t *testing.T) {
	now := time.Now()
	var sample trafficSample
	sample.update(1000, 2000, now)
	if got := sample.rate(now); got != 0 {
		t.Fatalf("first sample rate = %v", got)
	}
	sample.update(5000, 8000, now.Add(2*time.Second))
	if got := sample.rate(now.Add(2 * time.Second)); got != 5000 {
		t.Fatalf("rate = %v, want 5000 bytes/s over the real two-second interval", got)
	}
	if got := sample.rate(now.Add(6 * time.Second)); got != 5000 {
		t.Fatalf("rate expired at the four-second boundary: %v", got)
	}
	if got := sample.rate(now.Add(6*time.Second + time.Nanosecond)); got != 0 {
		t.Fatalf("stale rate = %v", got)
	}
	sample.update(10, 20, now.Add(7*time.Second))
	if got := sample.rate(now.Add(7 * time.Second)); got != 0 {
		t.Fatalf("counter reset rate = %v", got)
	}
	sample.update(20, 40, now.Add(8*time.Second))
	if got := sample.rate(now.Add(8 * time.Second)); got != 30 {
		t.Fatalf("rate after counter reset = %v", got)
	}
	sample.update(30, 50, now.Add(8*time.Second))
	if got := sample.rate(now.Add(8 * time.Second)); got != 0 || math.IsNaN(got) || math.IsInf(got, 0) {
		t.Fatalf("zero-interval rate = %v", got)
	}
}

func TestTrafficFramesDoNotEnterTheLog(t *testing.T) {
	a := testApp(t)
	a.connection = "connected"
	a.appendLog("normal log line")
	before, bytes := a.GetLogs(), a.logBytes
	for _, line := range []string{"desktop-traffic 100 200", "desktop-traffic -1 2", "desktop-traffic invalid"} {
		a.appendLog(line)
	}
	after := a.GetLogs()
	if len(after.Lines) != len(before.Lines) || after.Revision != before.Revision || a.logBytes != bytes {
		t.Fatal("machine telemetry changed the visible log or its revision")
	}
	if a.traffic.txBytes != 100 || a.traffic.rxBytes != 200 {
		t.Fatal("valid counters were not retained, or malformed telemetry replaced them")
	}
}

func TestTrafficStateAndDisconnectReset(t *testing.T) {
	a := testApp(t)
	a.connection = "connected"
	a.traffic = trafficSample{txBytes: 100, rxBytes: 200, at: time.Now(), bytesPerSecond: 300}
	if got := a.GetState().Connection.TrafficBytesPerSecond; got != 300 {
		t.Fatalf("connected rate = %v", got)
	}
	a.session = &backendSession{stopFile: filepath.Join(a.root, "stop"), done: make(chan struct{})}
	state, err := a.Disconnect()
	if err != nil || state.Connection.TrafficBytesPerSecond != 0 || !a.traffic.at.IsZero() {
		t.Fatalf("disconnect retained traffic: %+v, %v", state.Connection, err)
	}
	a.appendLog("desktop-traffic 99999 99999")
	if !a.traffic.at.IsZero() {
		t.Fatal("telemetry after disconnect recreated the previous session's sample")
	}
	a.connection = "credentials"
	a.appendLog("desktop-traffic 88888 88888")
	a.appendLog("TURN relay allocated: local")
	a.appendLog("first connection up")
	a.appendLog("DEFAULT ROUTE through the tunnel")
	a.appendLog("desktop-traffic 1 2")
	if got := a.GetState().Connection.TrafficBytesPerSecond; got != 0 {
		t.Fatalf("new session inherited previous traffic: %v", got)
	}
}

func TestTrafficBackendExit(t *testing.T) {
	if os.Getenv("VK_TURN_TRAFFIC_TEST_EXIT") == "1" {
		os.Exit(0)
	}
}

func TestBackendExitClearsTraffic(t *testing.T) {
	a := testApp(t)
	a.connection = "connected"
	a.traffic = trafficSample{at: time.Now(), bytesPerSecond: 1000}
	directory := filepath.Join(a.root, "session")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(os.Args[0], "-test.run=^TestTrafficBackendExit$")
	command.Env = append(os.Environ(), "VK_TURN_TRAFFIC_TEST_EXIT=1")
	command.Stdout, command.Stderr = writer, writer
	if err := command.Start(); err != nil {
		reader.Close()
		writer.Close()
		t.Fatal(err)
	}
	writer.Close()
	session := &backendSession{done: make(chan struct{})}
	a.session = session
	a.waitBackend(command, func() {}, reader, directory, session)
	if got := a.GetState().Connection; got.State != "disconnected" || got.TrafficBytesPerSecond != 0 || !a.traffic.at.IsZero() {
		t.Fatalf("backend exit retained traffic: %+v", got)
	}
}

func TestTrafficDirectionalRatesInConnectionState(t *testing.T) {
	a := testApp(t)
	a.connection = "connected"
	now := time.Now()
	a.traffic.update(1000, 2000, now.Add(-2*time.Second))
	a.traffic.update(5000, 8000, now)
	state := a.GetState().Connection
	if state.UploadBytesPerSecond != 2000 || state.DownloadBytesPerSecond != 3000 || state.TrafficBytesPerSecond != 5000 {
		t.Fatalf("directional rates = %+v", state)
	}
	a.retryError = "486"
	state = a.GetState().Connection
	if state.UploadBytesPerSecond != 0 || state.DownloadBytesPerSecond != 0 || state.TrafficBytesPerSecond != 0 {
		t.Fatalf("retry retained rates = %+v", state)
	}
	a.retryError = ""
	a.traffic.at = now.Add(-5 * time.Second)
	state = a.GetState().Connection
	if state.UploadBytesPerSecond != 0 || state.DownloadBytesPerSecond != 0 {
		t.Fatalf("stale directional rates = %+v", state)
	}
	a.traffic.update(1, 1, now)
	if a.traffic.txBytesPerSecond != 0 || a.traffic.rxBytesPerSecond != 0 {
		t.Fatal("counter reset retained directional rates")
	}
}
