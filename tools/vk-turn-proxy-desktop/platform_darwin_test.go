//go:build darwin

// SPDX-License-Identifier: MIT

package main

import (
	"encoding/xml"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestMacElevationArgumentsRemainSeparate(t *testing.T) {
	backend := `/Applications/VK "Proxy".app/Contents/MacOS/console`
	args := []string{"-config", "/private/profile with spaces\n'quoted'.json", "-parent-pid", "123"}
	command := macCommand(backend, args, "/tmp/private-socket", "/tmp/private-session/stop")
	if command.Path != "/bin/sh" {
		t.Fatalf("unexpected launcher: %s", command.Path)
	}
	if command.Args[7] != backend || !slices.Equal(command.Args[8:12], args) {
		t.Fatalf("backend arguments were transformed: %#v", command.Args)
	}
	if !slices.Equal(command.Args[len(command.Args)-2:], []string{"-desktop-log-socket", "/tmp/private-socket/log.sock"}) {
		t.Fatal("private log socket argument missing")
	}
	if !strings.Contains(macElevationScript, "quoted form of argument") {
		t.Fatal("AppleScript arguments must use native shell quoting")
	}
	if !command.SysProcAttr.Setpgid {
		t.Fatal("authorization helpers must own their process group")
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

func TestMacSocketPeerIdentity(t *testing.T) {
	directory, err := os.MkdirTemp("/tmp", "vk-turn-peer-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(directory)
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(directory, "log.sock"), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	client, err := net.DialUnix("unix", nil, listener.Addr().(*net.UnixAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	connection, err := listener.AcceptUnix()
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if got := rootSocketPeer(connection); got != (os.Geteuid() == 0) {
		t.Fatalf("unexpected peer identity: %v", got)
	}
}

func TestMacAuthorizationLauncherLifecycle(t *testing.T) {
	for _, mode := range []string{"normal", "cancel", "parent-exit"} {
		t.Run(mode, func(t *testing.T) {
			directory := t.TempDir()
			stopFile := filepath.Join(directory, "stop")
			reader, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer writer.Close()
			script := `return "ready"`
			if mode != "normal" {
				script = "delay 60"
			}
			launcher := strings.Replace(macElevationLauncher, "/usr/bin/osascript -e", ": > \"$directory/ready\"\n/usr/bin/osascript -e", 1)
			command := exec.Command("/bin/sh", "-c", launcher, "fixture", directory, stopFile, script)
			command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			command.ExtraFiles = []*os.File{reader}
			command.Stdout, command.Stderr = io.Discard, io.Discard
			if err := command.Start(); err != nil {
				reader.Close()
				t.Fatal(err)
			}
			reader.Close()
			var exitErr error
			done := make(chan struct{})
			go func() { exitErr = command.Wait(); close(done) }()
			t.Cleanup(func() {
				writer.Close()
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
					<-done
				}
			})
			if mode != "normal" {
				deadline := time.Now().Add(5 * time.Second)
				for {
					if _, err := os.Stat(filepath.Join(directory, "ready")); err == nil {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("authorization launcher did not initialize")
					}
					time.Sleep(10 * time.Millisecond)
				}
				if mode == "cancel" {
					if err := os.WriteFile(stopFile, nil, 0600); err != nil {
						t.Fatal(err)
					}
					_ = command.Process.Signal(syscall.SIGTERM)
				} else {
					writer.Close()
				}
			}
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("authorization launcher did not exit")
			}
			if (exitErr != nil) != (mode == "parent-exit") {
				t.Fatalf("unexpected %s exit: %v", mode, exitErr)
			}
			if _, err := os.Stat(directory); !os.IsNotExist(err) {
				t.Fatal("authorization launcher left its private directory")
			}
		})
	}
}
