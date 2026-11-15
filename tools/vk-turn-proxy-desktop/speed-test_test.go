// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSpeedServerSelection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer server.Close()
	servers := []SpeedTestServer{{ID: "test", Name: "Test", baseURL: server.URL + "/"}}
	selected, latency, err := selectSpeedServer(context.Background(), server.Client(), "auto", servers)
	if err != nil || selected.ID != "test" || latency < 0 {
		t.Fatalf("selection = %v, %v, %v", selected, latency, err)
	}
	_, _, err = selectSpeedServer(context.Background(), server.Client(), "missing", servers)
	if err == nil {
		t.Fatal("missing server accepted")
	}
}

func TestMeasureSpeed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			_, _ = io.Copy(io.Discard, r.Body)
			return
		}
		_, _ = w.Write(make([]byte, 64*1024))
	}))
	defer server.Close()
	for _, upload := range []bool{false, true} {
		rate, err := measureSpeed(context.Background(), server.Client(), server.URL+"/", upload, 150*time.Millisecond, func(float64) {})
		if err != nil || rate <= 0 {
			t.Fatalf("upload=%v rate=%v error=%v", upload, rate, err)
		}
	}
}

func TestMeasureSpeedFailureAndCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
	defer server.Close()
	_, err := measureSpeed(context.Background(), server.Client(), server.URL+"/", false, time.Second, func(float64) {})
	if err == nil {
		t.Fatal("HTTP failure accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = measureSpeed(ctx, server.Client(), server.URL+"/", false, time.Second, func(float64) {})
	if err != context.Canceled {
		t.Fatalf("cancellation = %v", err)
	}
}

func TestStartSpeedTestValidation(t *testing.T) {
	a := &App{ctx: context.Background(), connection: "disconnected"}
	if _, err := a.StartSpeedTest("untrusted-url"); err == nil {
		t.Fatal("unknown server accepted")
	}
	a.connection = "handshake"
	if _, err := a.StartSpeedTest("auto"); err == nil {
		t.Fatal("connection transition accepted")
	}
	a.connection = "connected"
	a.speedTest.Running = true
	if _, err := a.StartSpeedTest("auto"); err == nil {
		t.Fatal("concurrent test accepted")
	}
	state := a.GetSpeedTest()
	state.Servers[0].Name = "changed"
	if a.GetSpeedTest().Servers[0].Name == "changed" {
		t.Fatal("snapshot shares server storage")
	}
}

func TestMeasureSpeedRejectsHTML(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, "<html>The speed test server is unavailable. Please retry later.</html>")
	}))
	defer server.Close()
	for _, upload := range []bool{false, true} {
		_, err := measureSpeed(context.Background(), server.Client(), server.URL+"/", upload, 100*time.Millisecond, func(float64) {})
		if err == nil {
			t.Fatalf("upload=%v accepted HTML", upload)
		}
	}
}

func TestMeasureSpeedRetainsPartialUpload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
	}))
	defer server.Close()
	rate, err := measureSpeed(context.Background(), server.Client(), server.URL+"/", true, 100*time.Millisecond, func(float64) {})
	if err != nil || rate <= 0 {
		t.Fatalf("partial upload rate=%v error=%v", rate, err)
	}
}
