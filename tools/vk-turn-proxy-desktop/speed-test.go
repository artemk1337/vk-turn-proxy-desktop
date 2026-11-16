// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type SpeedTestServer struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	baseURL string
}

var speedTestServers = []SpeedTestServer{
	{ID: "amsterdam", Name: "Amsterdam · Clouvider", baseURL: "https://ams.speedtest.clouvider.net/backend/"},
	{ID: "london", Name: "London · Clouvider", baseURL: "https://lon.speedtest.clouvider.net/backend/"},
	{ID: "new-york", Name: "New York · Clouvider", baseURL: "https://nyc.speedtest.clouvider.net/backend/"},
	{ID: "prague", Name: "Prague · CESNET", baseURL: "https://speedtest.cesnet.cz/backend/"},
}

type SpeedTestState struct {
	Servers           []SpeedTestServer `json:"servers"`
	Phase             string            `json:"phase"`
	Running           bool              `json:"running"`
	ServerID          string            `json:"serverID"`
	LatencyMs         float64           `json:"latencyMs"`
	DownloadMbps      float64           `json:"downloadMbps"`
	UploadMbps        float64           `json:"uploadMbps"`
	Error             string            `json:"error"`
	FinishedAt        string            `json:"finishedAt"`
	LatencyProgress   float64           `json:"latencyProgress"`
	DownloadProgress  float64           `json:"downloadProgress"`
	UploadProgress    float64           `json:"uploadProgress"`
	DownloadStartedAt int64             `json:"downloadStartedAt"`
	UploadStartedAt   int64             `json:"uploadStartedAt"`
	MeasurementMs     int64             `json:"measurementMs"`
}

func (a *App) speedTestSnapshot() SpeedTestState {
	state := a.speedTest
	state.Servers = append([]SpeedTestServer{{ID: "auto", Name: "Automatic"}}, speedTestServers...)
	if state.Phase == "" {
		state.Phase = "idle"
	}
	return state
}

func (a *App) GetSpeedTest() SpeedTestState {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.speedTestSnapshot()
}

func (a *App) StartSpeedTest(serverID string) (SpeedTestState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closing || a.speedTest.Running {
		return a.speedTestSnapshot(), errors.New("A speed test is already running or the application is closing.")
	}
	if a.retryError != "" || (a.connection != "connected" && a.connection != "disconnected" && a.connection != "failed") {
		return a.speedTestSnapshot(), errors.New("Wait for the connection to finish changing.")
	}
	if serverID != "auto" && !slices.ContainsFunc(speedTestServers, func(server SpeedTestServer) bool { return server.ID == serverID }) {
		return a.speedTestSnapshot(), errors.New("Select a speed test server.")
	}
	ctx, cancel := context.WithTimeout(a.ctx, 45*time.Second)
	a.speedCancel = cancel
	a.speedRun++
	run := a.speedRun
	a.speedTest = SpeedTestState{Phase: "latency", Running: true, ServerID: serverID, MeasurementMs: 6000}
	go a.runSpeedTest(ctx, cancel, run, serverID)
	return a.speedTestSnapshot(), nil
}

func (a *App) cancelSpeedTestLocked() {
	if a.speedCancel != nil {
		a.speedCancel()
	}
}

func (a *App) StopSpeedTest() SpeedTestState {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cancelSpeedTestLocked()
	return a.speedTestSnapshot()
}

func (a *App) runSpeedTest(ctx context.Context, cancel context.CancelFunc, run uint64, serverID string) {
	defer cancel()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DisableCompression = true
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 12 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	update := func(change func(*SpeedTestState)) {
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.speedRun == run {
			change(&a.speedTest)
		}
	}
	server, latency, err := selectSpeedServer(ctx, client, serverID, speedTestServers, func(progress float64) {
		update(func(state *SpeedTestState) { state.LatencyProgress = progress })
	})
	if err == nil {
		update(func(state *SpeedTestState) {
			state.ServerID = server.ID
			state.LatencyMs = latency
			state.Phase = "download"
			state.DownloadStartedAt = time.Now().UnixMilli()
		})
		var rate float64
		rate, err = measureSpeed(ctx, client, server.baseURL, false, 6*time.Second, func(rate, progress float64) {
			update(func(state *SpeedTestState) { state.DownloadMbps = rate; state.DownloadProgress = progress })
		})
		if err == nil {
			update(func(state *SpeedTestState) {
				state.DownloadMbps = rate
				state.DownloadProgress = 1
				state.Phase = "upload"
				state.UploadStartedAt = time.Now().UnixMilli()
			})
			rate, err = measureSpeed(ctx, client, server.baseURL, true, 6*time.Second, func(rate, progress float64) {
				update(func(state *SpeedTestState) { state.UploadMbps = rate; state.UploadProgress = progress })
			})
			if err == nil {
				update(func(state *SpeedTestState) { state.UploadMbps = rate; state.UploadProgress = 1 })
			}
		}
	}
	update(func(state *SpeedTestState) {
		state.Running = false
		state.FinishedAt = time.Now().Format(time.RFC3339)
		state.Phase = "done"
		if err != nil {
			state.Phase = "failed"
			state.Error = "The speed test server is unavailable. Try another server."
		}
		if errors.Is(ctx.Err(), context.Canceled) {
			state.Phase = "cancelled"
			state.Error = ""
		}
	})
}

func selectSpeedServer(ctx context.Context, client *http.Client, id string, servers []SpeedTestServer, progress func(float64)) (SpeedTestServer, float64, error) {
	type result struct {
		server  SpeedTestServer
		latency float64
		err     error
	}
	results := make(chan result, len(servers))
	count := 0
	for _, server := range servers {
		if id != "auto" && server.ID != id {
			continue
		}
		count++
		go func() {
			probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			start := time.Now()
			err := speedRequest(probeCtx, client, http.MethodGet, server.baseURL+"empty.php", nil)
			// Reuse the established TLS connection for the latency sample.
			if err == nil {
				start = time.Now()
				err = speedRequest(probeCtx, client, http.MethodGet, server.baseURL+"empty.php", nil)
			}
			results <- result{server, float64(time.Since(start).Microseconds()) / 1000, err}
		}()
	}
	best := result{err: errors.New("No speed test server is available.")}
	for completed := range count {
		candidate := <-results
		progress(float64(completed+1) / float64(count))
		if candidate.err == nil && (best.err != nil || candidate.latency < best.latency) {
			best = candidate
		}
	}
	return best.server, best.latency, best.err
}

func speedRequest(ctx context.Context, client *http.Client, method, url string, body io.Reader) error {
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return err
	}
	req.Header.Set("Cache-Control", "no-cache, no-store")
	req.Header.Set("Content-Type", "application/octet-stream")
	response, err := client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("Speed test HTTP status %d", response.StatusCode)
	}
	size, err := io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if size > 32 {
		return errors.New("Unexpected speed test response.")
	}
	return err
}

func measureSpeed(ctx context.Context, client *http.Client, baseURL string, upload bool, duration time.Duration, progress func(float64, float64)) (float64, error) {
	phaseCtx, cancel := context.WithTimeout(ctx, duration)
	defer cancel()
	var total atomic.Int64
	var workers sync.WaitGroup
	errorsCh := make(chan error, 3)
	start := time.Now()
	for range 3 {
		workers.Go(func() {
			payload := make([]byte, 1<<20)
			rand.Read(payload)
			payloadSize := 16 * 1024
			for phaseCtx.Err() == nil {
				method, url := http.MethodGet, fmt.Sprintf("%sgarbage.php?ckSize=16&r=%d", baseURL, time.Now().UnixNano())
				var body io.Reader
				if upload {
					method, url, body = http.MethodPost, baseURL+"empty.php", bytes.NewReader(payload[:payloadSize])
				}
				req, err := http.NewRequestWithContext(phaseCtx, method, url, body)
				if err != nil {
					errorsCh <- err
					return
				}
				if upload {
					req.ContentLength = int64(payloadSize)
				}
				req.Header.Set("Cache-Control", "no-cache, no-store")
				req.Header.Set("Content-Type", "application/octet-stream")
				requestStart := time.Now()
				response, err := client.Do(req)
				if err != nil {
					if phaseCtx.Err() == nil {
						errorsCh <- err
					}
					return
				}
				if response.StatusCode < 200 || response.StatusCode >= 300 {
					response.Body.Close()
					errorsCh <- fmt.Errorf("Speed test HTTP status %d", response.StatusCode)
					return
				}
				if !upload && !strings.HasPrefix(response.Header.Get("Content-Type"), "application/octet-stream") {
					response.Body.Close()
					errorsCh <- errors.New("Unexpected download content type.")
					return
				}
				if upload {
					size, readErr := io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
					err = readErr
					if size > 32 {
						err = errors.New("Unexpected upload response.")
					}
					if err == nil {
						// A successful response confirms the completed upload. Adapt
						// request size to keep slow links measurable without counting buffers.
						total.Add(int64(payloadSize))
						payloadSize = max(4096, min(len(payload), int(float64(payloadSize)*0.5/time.Since(requestStart).Seconds())))
					}
				} else {
					buffer := make([]byte, 32*1024)
					for {
						n, readErr := response.Body.Read(buffer)
						total.Add(int64(n))
						if readErr != nil {
							err = readErr
							break
						}
					}
				}
				response.Body.Close()
				if err != nil && !errors.Is(err, io.EOF) {
					if phaseCtx.Err() == nil {
						errorsCh <- err
					}
					return
				}
			}
		})
	}
	done := make(chan struct{})
	go func() { workers.Wait(); close(done) }()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			progress(float64(total.Load())*8/time.Since(start).Seconds()/1e6, min(1, time.Since(start).Seconds()/duration.Seconds()))
		case <-done:
			rate := float64(total.Load()) * 8 / time.Since(start).Seconds() / 1e6
			if ctx.Err() != nil {
				return rate, ctx.Err()
			}
			select {
			case err := <-errorsCh:
				return rate, err
			default:
			}
			if total.Load() == 0 {
				return 0, errors.New("The speed test transferred no data.")
			}
			return rate, nil
		}
	}
}
