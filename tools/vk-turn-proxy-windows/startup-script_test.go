// SPDX-License-Identifier: MIT

package main

import (
	"strings"
	"testing"
)

func TestAutostartScriptsKeepPathsAsData(t *testing.T) {
	path := `C:\Users\O'Neil\VK TURN Proxy\client.exe`
	script := startupTaskScript(path, "S-1-5-21-test", "test-task", "enable")
	if !strings.Contains(script, "O''Neil") || !strings.Contains(script, "-LogonType Interactive -RunLevel Highest") || !strings.Contains(script, "-AtLogOn -User $sid") || !strings.Contains(script, "'--autostart'") {
		t.Fatal("autostart task lost quoting, user scope, or interactive elevation")
	}
	if strings.Contains(script, "-Password") {
		t.Fatal("autostart must not store a password")
	}
}

func TestAutostartDisableRequiresTheMatchingExecutable(t *testing.T) {
	script := startupTaskScript("client.exe", "sid", "test-task", "disable")
	if !strings.Contains(script, "if ($same) { Unregister-ScheduledTask") || !strings.Contains(script, "OrdinalIgnoreCase") || !strings.Contains(script, "@($task.Actions).Count -eq 1") {
		t.Fatal("disable can affect an unrelated startup task")
	}
}

func TestAutostartQueryRequiresEnabledMatchingTask(t *testing.T) {
	script := startupTaskScript("client.exe", "sid", "test-task", "query")
	if !strings.Contains(script, "$same -and $task.State -ne 'Disabled'") {
		t.Fatal("query ignores task ownership or state")
	}
}

func TestAutostartScriptsSuppressProgressAndUseUTF8(t *testing.T) {
	for _, operation := range []string{"query", "enable", "disable"} {
		script := startupTaskScript("client.exe", "sid", "test-task", operation)
		if !strings.HasPrefix(script, "$ErrorActionPreference='Stop'; $ProgressPreference='SilentlyContinue'; ") {
			t.Fatalf("%s can emit module progress into machine output", operation)
		}
		if !strings.Contains(script, "[Console]::OutputEncoding=[Text.UTF8Encoding]::new($false)") || !strings.Contains(script, "$OutputEncoding=[Console]::OutputEncoding") {
			t.Fatalf("%s does not use UTF-8 for redirected output", operation)
		}
	}
}
