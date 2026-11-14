// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestAutostartTaskLifecycle(t *testing.T) {
	if !windows.GetCurrentProcessToken().IsElevated() {
		t.Skip("scheduled task creation requires an elevated process")
	}
	sid, err := startupUserSID()
	if err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(t.TempDir(), "client.exe")
	if err := os.WriteFile(executable, nil, 0600); err != nil {
		t.Fatal(err)
	}
	taskName := fmt.Sprintf("VKTurnProxyWindows-Test-%d", os.Getpid())
	t.Cleanup(func() {
		if _, err := runStartupTask(executable, sid, taskName, "disable"); err != nil {
			t.Error(err)
		}
	})
	if _, err := runStartupTask(executable, sid, taskName, "enable"); err != nil {
		t.Fatal(err)
	}
	out, err := runStartupTask(executable, sid, taskName, "query")
	if err != nil || strings.TrimSpace(out) != "true" {
		t.Fatalf("registered task was not enabled: %q, %v", out, err)
	}
	other := filepath.Join(t.TempDir(), "other.exe")
	if _, err := runStartupTask(other, sid, taskName, "disable"); err != nil {
		t.Fatal(err)
	}
	out, err = runStartupTask(executable, sid, taskName, "query")
	if err != nil || strings.TrimSpace(out) != "true" {
		t.Fatal("disabling another location removed the registered task")
	}
	if _, err := runStartupTask(executable, sid, taskName, "disable"); err != nil {
		t.Fatal(err)
	}
	out, err = runStartupTask(executable, sid, taskName, "query")
	if err != nil || strings.TrimSpace(out) != "false" {
		t.Fatalf("removed task still enabled: %q, %v", out, err)
	}
}
