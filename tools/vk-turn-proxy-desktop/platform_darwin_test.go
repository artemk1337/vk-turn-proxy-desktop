//go:build darwin

// SPDX-License-Identifier: MIT

package main

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestMacHelperArgumentsRemainSeparate(t *testing.T) {
	backend := `/Applications/VK "Proxy".app/Contents/MacOS/console`
	args := []string{"-config", "/private/profile with spaces\n'quoted'.json", "-parent-pid", "123"}
	command := macCommand(backend, args)
	if command.Path != backend {
		t.Fatalf("unexpected client: %s", command.Path)
	}
	if command.Args[1] != "--desktop-helper-client" || !slices.Equal(command.Args[2:], args) {
		t.Fatalf("backend arguments were transformed: %#v", command.Args)
	}
}

func TestMacLaunchAgentRoundTrip(t *testing.T) {
	executable := `/Applications/VK <Proxy> & "Friends".app/Contents/MacOS/client`
	data, err := macLaunchAgent(executable)
	if err != nil {
		t.Fatal(err)
	}
	var document any
	if err := xml.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	enabled, err := macLaunchAgentMatches(data, executable)
	if err != nil || !enabled {
		t.Fatalf("agent was not recognized: %v, %v", enabled, err)
	}
	if enabled, err := macLaunchAgentMatches(data, "/another/client"); err != nil || enabled {
		t.Fatalf("agent for another executable was accepted: %v, %v", enabled, err)
	}
	disabled := strings.ReplaceAll(string(data), "<true/>", "<false/>")
	if enabled, err := macLaunchAgentMatches([]byte(disabled), executable); err != nil || enabled {
		t.Fatalf("disabled agent was accepted: %v, %v", enabled, err)
	}
	if _, err := macLaunchAgentMatches([]byte("invalid XML"), executable); err == nil {
		t.Fatal("malformed agent should return an error")
	}
}

func TestMacHelperLaunchFailure(t *testing.T) {
	output, err := os.CreateTemp(t.TempDir(), "log")
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	command, finish, err := startPlatformCommand("/missing/vk-console", nil, output, "")
	if err == nil || command != nil || finish != nil {
		t.Fatalf("unexpected launch result: %v, %v", command, err)
	}
}

func TestMacHelperParentPipeLifecycle(t *testing.T) {
	// A shell fixture reads the inherited parent pipe using the same fd as
	// the console client. Closing it must unblock the child.
	fixture := filepath.Join(t.TempDir(), "console")
	if err := os.WriteFile(fixture, []byte("#!/bin/sh\nread -r line <&3\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	output, err := os.CreateTemp(t.TempDir(), "log")
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	command, finish, err := startPlatformCommand(fixture, nil, output, "")
	if err != nil {
		t.Fatal(err)
	}
	finish()
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		command.Process.Kill()
		t.Fatal("helper client did not observe parent pipe closure")
	}
}
