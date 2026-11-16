// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestReleaseVersionComparison(t *testing.T) {
	for _, tc := range []struct {
		next, current string
		newer         bool
	}{
		{"v1.10.1", "v1.10.0", true},
		{"v1.10.0", "1.9.9", true},
		{"v2.0.0", "v1.99.99", true},
		{"v1.10.0", "v1.10.0", false},
		{"v1.9.9", "v1.10.0", false},
		{"v1.10.1-beta.1", "v1.10.0", false},
		{"v1.10.1", "dev", false},
		{"v01.10.1", "v1.10.0", false},
		{"v99999999999999999999999.0.0", "v1.0.0", false},
		{"v1.10.1/path", "v1.10.0", false},
	} {
		if got := releaseIsNewer(tc.next, tc.current); got != tc.newer {
			t.Errorf("releaseIsNewer(%q, %q) = %t, want %t", tc.next, tc.current, got, tc.newer)
		}
	}
}

func TestRequestLatestRelease(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		valid      bool
	}{
		{"stable", `{"tag_name":"v1.11.0","draft":false,"prerelease":false}`, 200, true},
		{"draft", `{"tag_name":"v1.11.0","draft":true}`, 200, false},
		{"prerelease", `{"tag_name":"v1.11.0","prerelease":true}`, 200, false},
		{"invalid version", `{"tag_name":"v1.11.0-beta.1"}`, 200, false},
		{"invalid json", `{"tag_name":`, 200, false},
		{"missing tag", `{}`, 200, false},
		{"rate limit", `{}`, 403, false},
		{"missing release", `{}`, 404, false},
		{"oversized", strings.Repeat(" ", maxReleaseBytes+1), 200, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Accept") != "application/vnd.github+json" || r.Header.Get("User-Agent") != "VK-TURN-Proxy-Desktop" {
					t.Error("GitHub request headers are missing")
				}
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			tag, err := requestLatestRelease(context.Background(), server.Client(), server.URL)
			if (err == nil) != tc.valid || tc.valid && tag != "v1.11.0" {
				t.Fatalf("unexpected release %q, error %v", tag, err)
			}
		})
	}
}

func TestRequestLatestReleaseCancellation(t *testing.T) {
	entered := make(chan struct{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	completed := make(chan error, 1)
	go func() { _, err := requestLatestRelease(ctx, server.Client(), server.URL); completed <- err }()
	<-entered
	cancel()
	select {
	case err := <-completed:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected canceled request, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("update request ignored cancellation")
	}
}

func releaseTestApp(t *testing.T, current string) *App {
	t.Helper()
	previous := version
	version = current
	t.Cleanup(func() { version = previous })
	app := testApp(t)
	app.updates = initialUpdateState()
	return app
}

func awaitUpdateStatus(t *testing.T, app *App, expected string) State {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		state := app.GetState()
		if state.Updates.Status == expected {
			return state
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("expected update state %q, got %+v", expected, app.GetState().Updates)
	return State{}
}

func TestAppUpdateCheckIsAsynchronousAndDeduplicated(t *testing.T) {
	app := releaseTestApp(t, "v1.10.0")
	entered := make(chan struct{})
	release := make(chan struct{})
	var requests atomic.Int32
	app.updateFetch = func(ctx context.Context) (string, error) {
		if requests.Add(1) == 1 {
			close(entered)
		}
		select {
		case <-release:
			return "v1.11.0", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	state := app.CheckForUpdates()
	if state.Updates.Status != "checking" || state.Updates.CurrentVersion != "v1.10.0" {
		t.Fatalf("check did not return immediately with checking state: %+v", state.Updates)
	}
	<-entered
	for range 10 {
		app.CheckForUpdates()
	}
	if requests.Load() != 1 {
		t.Fatal("concurrent update requests were duplicated")
	}
	close(release)
	state = awaitUpdateStatus(t, app, "available")
	if state.Updates.LatestVersion != "v1.11.0" || state.Updates.URL != releasePage+"v1.11.0" {
		t.Fatalf("unexpected release state %+v", state.Updates)
	}
	app.checkForUpdates(false)
	if requests.Load() != 1 {
		t.Fatal("automatic check ignored daily interval")
	}
	app.CheckForUpdates()
	awaitUpdateStatus(t, app, "available")
	if requests.Load() != 2 {
		t.Fatal("manual check did not bypass the interval")
	}
}

func TestAppUpdateStatusAndCancellation(t *testing.T) {
	for _, tc := range []struct {
		name, latest, expected string
		err                    error
	}{
		{"same version", "v1.10.0", "current", nil},
		{"older version", "v1.9.9", "current", nil},
		{"network failure", "", "error", errors.New("network unavailable")},
		{"canceled", "", "error", context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := releaseTestApp(t, "v1.10.0")
			app.updateFetch = func(context.Context) (string, error) { return tc.latest, tc.err }
			app.CheckForUpdates()
			state := awaitUpdateStatus(t, app, tc.expected)
			if state.Updates.URL != "" {
				t.Fatal("non-available release exposed a download URL")
			}
		})
	}
}

func TestDevelopmentBuildDoesNotCheck(t *testing.T) {
	app := releaseTestApp(t, "dev")
	app.updateFetch = func(context.Context) (string, error) { t.Error("development build queried GitHub"); return "", nil }
	if state := app.CheckForUpdates(); state.Updates.Status != "development" {
		t.Fatalf("unexpected development status %+v", state.Updates)
	}
}

func TestAppUpdateCheckUsesApplicationCancellation(t *testing.T) {
	app := releaseTestApp(t, "v1.10.0")
	entered := make(chan struct{})
	app.updateFetch = func(ctx context.Context) (string, error) {
		close(entered)
		<-ctx.Done()
		return "", ctx.Err()
	}
	app.CheckForUpdates()
	<-entered
	app.cancel()
	awaitUpdateStatus(t, app, "error")
}

func TestClosingAppDoesNotStartUpdateCheck(t *testing.T) {
	app := releaseTestApp(t, "v1.10.0")
	app.closing = true
	app.updateFetch = func(context.Context) (string, error) {
		t.Error("closing app queried GitHub")
		return "", nil
	}
	if state := app.CheckForUpdates(); state.Updates.Status != "idle" {
		t.Fatalf("closing app changed update state %+v", state.Updates)
	}
}
