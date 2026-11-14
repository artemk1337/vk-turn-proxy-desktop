//go:build windows

package main

import (
	"os/exec"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsPidFileExcludesConcurrentClient(t *testing.T) {
	path := filepath.Join(t.TempDir(), "client.pid")
	first, err := takePidFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if second, err := takePidFile(path); err == nil {
		second.release()
		first.release()
		t.Fatal("a second client acquired the same pid file")
	}
	first.release()
	third, err := takePidFile(path)
	if err != nil {
		t.Fatal("released lock must be reusable", err)
	}
	third.release()
}

func TestWindowsSubprocessesDoNotOpenConsoleWindows(t *testing.T) {
	cmd := exec.Command("powershell.exe")
	hideCommandWindow(cmd)
	if !cmd.SysProcAttr.HideWindow || cmd.SysProcAttr.CreationFlags&windows.CREATE_NO_WINDOW == 0 {
		t.Fatal("PowerShell must stay hidden behind the desktop client")
	}
}

func TestWindowsCanReadDefaultRoute(t *testing.T) {
	out, err := runCmd(windowsDefaultRouteCommand())
	if err != nil {
		t.Fatal("PowerShell default-route query failed", err)
	}
	if _, ok := parseWindowsDefaultRoute(out); !ok {
		t.Skip("this host has no physical IPv4 default route")
	}
}
