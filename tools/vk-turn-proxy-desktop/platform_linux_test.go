//go:build linux

// SPDX-License-Identifier: MIT
//
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLinuxAutostartLifecycle(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	launcher := filepath.Join(t.TempDir(), `VK $ app % "quoted"`)
	t.Setenv("VKTURN_DESKTOP_LAUNCHER", launcher)
	if err := setAutostart(true, "/ignored/binary"); err != nil {
		t.Fatal(err)
	}
	enabled, err := autostartEnabled("/ignored/binary")
	if err != nil || !enabled {
		t.Fatalf("enabled = %v, %v", enabled, err)
	}
	path, err := autostartPath()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "TryExec="+launcher+"\n") {
		t.Fatal("launcher missing")
	}
	if !strings.Contains(string(data), `\$`) || !strings.Contains(string(data), `%%`) {
		t.Fatal("Exec path not escaped")
	}
	if err := setAutostart(false, "/ignored/binary"); err != nil {
		t.Fatal(err)
	}
	enabled, err = autostartEnabled("/ignored/binary")
	if err != nil || enabled {
		t.Fatalf("disabled = %v, %v", enabled, err)
	}
}
