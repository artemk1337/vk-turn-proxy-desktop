// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestUpdateAssetName(t *testing.T) {
	for _, platform := range []struct {
		os, name, suffix string
	}{
		{"windows", "windows", "-setup.exe"},
		{"linux", "linux", ".tar.gz"},
		{"darwin", "macos", ".zip"},
	} {
		for _, arch := range []string{"amd64", "arm64"} {
			name, err := updateAssetName("v1.11.0", platform.os, arch)
			want := "vk-turn-proxy-" + platform.name + "-v1.11.0-" + arch + platform.suffix
			if err != nil || name != want {
				t.Fatalf("updateAssetName(%q, %q) = %q, %v, want %q", platform.os, arch, name, err, want)
			}
		}
	}
	for _, input := range [][3]string{
		{"v1.11.0/other", "linux", "amd64"},
		{"v1.11.0", "freebsd", "amd64"},
		{"v1.11.0", "linux", "386"},
	} {
		if _, err := updateAssetName(input[0], input[1], input[2]); err == nil {
			t.Fatalf("accepted unsupported asset parameters %q", input)
		}
	}
}

func TestDownloadUpdateFile(t *testing.T) {
	const body = "release contents"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != "VK-TURN-Proxy-Desktop" {
			t.Error("missing download user agent")
		}
		w.Header().Set("Content-Length", fmt.Sprint(len(body)))
		fmt.Fprint(w, body)
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "update.zip")
	var downloaded, total int64
	err := downloadUpdateFile(context.Background(), server.Client(), server.URL, path, func(done, size int64) {
		if done < downloaded {
			t.Error("download progress moved backwards")
		}
		downloaded, total = done, size
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != body || downloaded != int64(len(body)) || total != int64(len(body)) {
		t.Fatalf("download = %q, %v, progress %d/%d", got, err, downloaded, total)
	}
	assertNoPartialUpdates(t, filepath.Dir(path))
}

func TestDownloadUpdateFailurePreservesDestination(t *testing.T) {
	for _, scenario := range []string{"short response", "http error", "empty response", "oversized header", "canceled"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch scenario {
				case "http error":
					w.WriteHeader(http.StatusNotFound)
				case "empty response":
					w.Header().Set("Content-Length", "0")
				case "oversized header":
					w.Header().Set("Content-Length", fmt.Sprint(maxUpdateBytes+1))
				case "short response":
					w.Header().Set("Content-Length", "100")
					fmt.Fprint(w, "partial")
				case "canceled":
					fmt.Fprint(w, "partial")
					w.(http.Flusher).Flush()
					<-r.Context().Done()
				}
			}))
			defer server.Close()
			path := filepath.Join(t.TempDir(), "update.zip")
			if err := os.WriteFile(path, []byte("existing"), 0600); err != nil {
				t.Fatal(err)
			}
			err := downloadUpdateFile(ctx, server.Client(), server.URL, path, func(done, total int64) {
				if scenario == "canceled" {
					cancel()
				}
			})
			if err == nil {
				t.Fatal("expected failed download")
			}
			if scenario == "canceled" && !errors.Is(err, context.Canceled) {
				t.Fatalf("expected cancellation, got %v", err)
			}
			got, readErr := os.ReadFile(path)
			if readErr != nil || string(got) != "existing" {
				t.Fatalf("destination changed after failure: %q, %v", got, readErr)
			}
			assertNoPartialUpdates(t, filepath.Dir(path))
		})
	}
}

func assertNoPartialUpdates(t *testing.T, dir string) {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, ".update-*"))
	if err != nil || len(files) != 0 {
		t.Fatalf("partial downloads remain: %v, %v", files, err)
	}
}
