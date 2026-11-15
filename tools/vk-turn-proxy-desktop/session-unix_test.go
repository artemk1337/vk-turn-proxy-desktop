//go:build !windows

// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestSessionLifecycleAndExitCleanup(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("macOS elevation requires an interactive administrator prompt")
	}
	if runtime.GOOS == "linux" && os.Geteuid() != 0 {
		t.Skip("fixture connection needs a root Linux test process to bypass polkit")
	}
	a := testApp(t)
	a.backend = filepath.Join(t.TempDir(), "fixture-console")
	script := `#!/bin/sh
if [ "$1" = "-validate-profile" ]; then exit 0; fi
stop=""
while [ "$#" -gt 0 ]; do
  if [ "$1" = "-stop-file" ]; then shift; stop="$1"; fi
  shift
done
echo 'TURN relay allocated: fixture'
echo 'proxy: first connection up'
echo 'DEFAULT ROUTE → the tunnel'
while [ ! -f "$stop" ]; do sleep 0.02; done
echo 'cleanup complete'
`
	if err := os.WriteFile(a.backend, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	state, err := a.SaveProfile(Profile{Name: "Fixture", Mode: "link", ProfileText: "fixture-private-data"})
	if err != nil || len(state.Profiles) != 1 {
		t.Fatalf("save failed: %v", err)
	}
	if _, err := a.Connect(); err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	session := a.session
	a.mu.Unlock()
	deadline := time.After(5 * time.Second)
	for a.GetState().Connection.State != "connected" {
		select {
		case <-deadline:
			t.Fatal("backend never connected")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	exited := make(chan struct{})
	a.quit = func() { close(exited) }
	go a.Exit()
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		t.Fatal("exit did not wait for normal cleanup")
	}
	if _, err := os.Stat(filepath.Dir(session.stopFile)); !os.IsNotExist(err) {
		t.Fatal("private session files survived backend exit")
	}
	if a.GetState().Connection.State != "disconnected" {
		t.Fatal("backend exit state was not delivered")
	}
	logs := a.GetLogs()
	if len(logs.Lines) == 0 || logs.Lines[len(logs.Lines)-1] != "cleanup complete" {
		t.Fatal("final backend log was lost")
	}
}
