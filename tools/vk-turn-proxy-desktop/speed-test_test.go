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
	selected, latency, err := selectSpeedServer(context.Background(), server.Client(), "auto", servers, func(float64) {})
	if err != nil || selected.ID != "test" || latency < 0 {
		t.Fatalf("selection = %v, %v, %v", selected, latency, err)
	}
	_, _, err = selectSpeedServer(context.Background(), server.Client(), "missing", servers, func(float64) {})
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
		rate, err := measureSpeed(context.Background(), server.Client(), server.URL+"/", upload, 150*time.Millisecond, func(float64, float64) {})
		if err != nil || rate <= 0 {
			t.Fatalf("upload=%v rate=%v error=%v", upload, rate, err)
		}
	}
}

func TestMeasureSpeedFailureAndCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
	defer server.Close()
	_, err := measureSpeed(context.Background(), server.Client(), server.URL+"/", false, time.Second, func(float64, float64) {})
	if err == nil {
		t.Fatal("HTTP failure accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = measureSpeed(ctx, server.Client(), server.URL+"/", false, time.Second, func(float64, float64) {})
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
		_, err := measureSpeed(context.Background(), server.Client(), server.URL+"/", upload, 100*time.Millisecond, func(float64, float64) {})
		if err == nil {
			t.Fatalf("upload=%v accepted HTML", upload)
		}
	}
}

func TestMeasureSpeedRejectsUnconfirmedUpload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
	}))
	defer server.Close()
	rate, err := measureSpeed(context.Background(), server.Client(), server.URL+"/", true, 100*time.Millisecond, func(float64, float64) {})
	if err == nil || rate != 0 {
		t.Fatalf("unconfirmed upload rate=%v error=%v", rate, err)
	}
}

func TestSpeedServerSelectionProgress(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()
	servers := []SpeedTestServer{{ID: "one", baseURL: server.URL + "/"}, {ID: "two", baseURL: server.URL + "/"}}
	var updates []float64
	_, _, err := selectSpeedServer(context.Background(), server.Client(), "auto", servers, func(progress float64) {
		updates = append(updates, progress)
	})
	if err != nil || len(updates) != 2 || updates[0] != 0.5 || updates[1] != 1 {
		t.Fatalf("progress = %v, error %v", updates, err)
	}
}

func TestMeasureSpeedReportsAverageAndProgress(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case <-time.After(5 * time.Millisecond):
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	var rateUpdates int
	var previousProgress float64
	rate, err := measureSpeed(context.Background(), server.Client(), server.URL+"/", true, 350*time.Millisecond, func(rate, progress float64) {
		if rate < 0 || progress < previousProgress || progress < 0 || progress > 1 {
			t.Errorf("rate %v, progress %v after %v", rate, progress, previousProgress)
		}
		previousProgress = progress
		rateUpdates++
	})
	if err != nil || rate <= 0 || rateUpdates == 0 {
		t.Fatalf("rate=%v, updates=%d, error=%v", rate, rateUpdates, err)
	}
}
