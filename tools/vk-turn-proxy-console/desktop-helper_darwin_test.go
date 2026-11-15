//go:build darwin

// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"golang.org/x/sys/unix"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	routingpolicy "github.com/artemk1337/vk-turn-proxy-desktop/pkg/routing-policy"
)

func TestHelperRoutingSnapshotAndValidation(t *testing.T) {
	directory := t.TempDir()
	profile := filepath.Join(directory, "profile")
	routing := filepath.Join(directory, "routing.json")
	policy := []byte(`{"mode":"exclude","prefixes":["203.0.113.0/24"],"localDirect":true}`)
	for path, data := range map[string][]byte{profile: []byte("private profile"), routing: policy} {
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	options, err := parseFlags([]string{"-config", profile, "-routing-file", routing, "-block-ipv6=true"})
	if err != nil {
		t.Fatal(err)
	}
	request, err := helperRequestFromOptions(options)
	if err != nil || !bytes.Equal(request.Routing, policy) {
		t.Fatalf("routing snapshot missing: %v", err)
	}
	if err := validateHelperRequest(request); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(request.Args, " "), directory) {
		t.Fatal("user routing path was passed to the privileged console")
	}
	request.Routing = []byte(`{"mode":"include","prefixes":["::/0"]}`)
	if err := validateHelperRequest(request); err == nil {
		t.Fatal("helper accepted an invalid routing policy")
	}
	request.Routing = make([]byte, routingpolicy.MaxPolicyBytes+1)
	if err := validateHelperRequest(request); err == nil {
		t.Fatal("helper accepted an oversized routing policy")
	}
	request.Routing = nil
	request.Args = []string{"-routing-file", "/tmp/untrusted"}
	if err := validateHelperRequest(request); err == nil {
		t.Fatal("helper accepted an untrusted routing path in Args")
	}
}

func TestHelperRequestRejectsPrivilegedArguments(t *testing.T) {
	for _, args := range [][]string{{"-log", "/tmp/root"}, {"-state-file", "/tmp/state"}, {"-config", "/etc/shadow"}, {"-cleanup"}, {"-peer"}, {"-server", strings.Repeat("x", 4097)}} {
		if err := validateHelperRequest(helperRequest{Profile: []byte("profile"), Args: args}); err == nil {
			t.Fatalf("accepted %q", args)
		}
	}
	if err := validateHelperRequest(helperRequest{Profile: []byte("profile"), Args: []string{"-peer", "example.org:443", "-block-ipv6=true"}}); err != nil {
		t.Fatal(err)
	}
	if err := validateHelperRequest(helperRequest{Profile: make([]byte, wizardInputLimit+1)}); err == nil {
		t.Fatal("accepted oversized profile")
	}
}

func TestHelperReadsInputsBeforeElevation(t *testing.T) {
	directory := t.TempDir()
	profile := filepath.Join(directory, "profile")
	if err := os.WriteFile(profile, []byte("private profile"), 0600); err != nil {
		t.Fatal(err)
	}
	options, err := parseFlags([]string{"-config", profile, "-parent-pid", "123", "-stop-file", "/tmp/stop", "-block-ipv6=true", "-desktop-traffic"})
	if err != nil {
		t.Fatal(err)
	}
	request, err := helperRequestFromOptions(options)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(request.Args, "-desktop-traffic=true") {
		t.Fatal("desktop traffic counters were not forwarded")
	}
	if err := validateHelperRequest(request); err != nil {
		t.Fatal(err)
	}
	if string(request.Profile) != "private profile" {
		t.Fatal("profile snapshot missing")
	}
	if strings.Contains(strings.Join(request.Args, " "), directory) {
		t.Fatal("user path forwarded to root")
	}
	options.set["state-file"] = true
	if _, err = helperRequestFromOptions(options); err == nil {
		t.Fatal("accepted root state override")
	}
}

func TestHelperInstallationCopiesAtomically(t *testing.T) {
	directory := t.TempDir()
	source, destination := filepath.Join(directory, "source"), filepath.Join(directory, "installed")
	if err := os.WriteFile(source, []byte("binary version 1"), 0600); err != nil {
		t.Fatal(err)
	}
	digest, err := helperDigest(source)
	if err != nil {
		t.Fatal(err)
	}
	if err = copyHelperExecutable(source, destination, digest); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(source, []byte("modified binary"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = copyHelperExecutable(source, destination, digest); err == nil {
		t.Fatal("accepted changed binary")
	}
	contents, err := os.ReadFile(destination)
	if err != nil || string(contents) != "binary version 1" {
		t.Fatal("failed copy replaced installed binary")
	}
	info, err := os.Stat(destination)
	if err != nil || info.Mode().Perm() != 0755 {
		t.Fatal("incorrect executable mode")
	}
	plist := helperLaunchPlist(501)
	for _, value := range []string{helperLabel, helperExecutable, "--desktop-helper-serve", "<string>501</string>"} {
		if !strings.Contains(plist, value) {
			t.Fatalf("plist missing %s", value)
		}
	}
	if err = writeHelperFile(filepath.Join(directory, "daemon.plist"), []byte(plist), 0644); err != nil {
		t.Fatal(err)
	}
}

func helperTestSockets(t *testing.T) (*net.UnixConn, *net.UnixConn) {
	t.Helper()
	directory, err := os.MkdirTemp("/tmp", "vk-helper-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(directory) })
	path := filepath.Join(directory, "s")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	client, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	server, err := listener.AcceptUnix()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close(); server.Close() })
	return client, server
}

func TestHelperProtocolLogFrames(t *testing.T) {
	client, server := helperTestSockets(t)
	uid, err := helperPeerUID(client)
	if err != nil || uid != uint32(os.Getuid()) {
		t.Fatalf("peer uid %d: %v", uid, err)
	}
	writer := helperLogWriter{connection: server, encoder: json.NewEncoder(server)}
	data := strings.Repeat("я", 5000)
	done := make(chan error, 1)
	go func() {
		_, err := writer.Write([]byte(data))
		if err == nil {
			_, err = writer.Write([]byte{0xd1})
		}
		if err == nil {
			_, err = writer.Write([]byte{0x8f})
		}
		server.CloseWrite()
		done <- err
	}()
	scanner := bufio.NewScanner(client)
	var received strings.Builder
	for scanner.Scan() {
		var frame helperFrame
		if err = json.Unmarshal(scanner.Bytes(), &frame); err != nil {
			t.Fatal(err)
		}
		if len(frame.Log) > 8192 {
			t.Fatal("oversized log frame")
		}
		received.Write(frame.Log)
	}
	if err = scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if received.String() != data+"я" {
		t.Fatal("log content changed")
	}
}

func TestHelperCancellationWaitsForCleanup(t *testing.T) {
	for _, mode := range []string{"context", "disconnect"} {
		t.Run(mode, func(t *testing.T) {
			client, server := helperTestSockets(t)
			command := exec.Command("/bin/sh", "-c", `trap 'sleep 0.05; echo cleaned; exit 0' TERM; echo ready; while :; do sleep 0.05; done`)
			output, err := command.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err = command.Start(); err != nil {
				t.Fatal(err)
			}
			defer command.Process.Kill()
			reader := bufio.NewReader(output)
			if line, err := reader.ReadString('\n'); err != nil || line != "ready\n" {
				t.Fatal("child not ready")
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan struct{})
			go watchHelperConsole(ctx, server, command, done)
			if mode == "context" {
				cancel()
			} else {
				client.CloseWrite()
			}
			finished := make(chan error, 1)
			var captured bytes.Buffer
			go func() { _, _ = captured.ReadFrom(reader); finished <- command.Wait(); close(done) }()
			select {
			case err := <-finished:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("cleanup timed out")
			}
			if captured.String() != "cleaned\n" {
				t.Fatalf("cleanup missing: %q", captured.String())
			}
		})
	}
}

func TestHelperInputRejectsSymlinksAndPipes(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, "source")
	if err := os.WriteFile(source, []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(directory, "link")
	if err := os.Symlink(source, link); err != nil {
		t.Fatal(err)
	}
	if _, err := readHelperInput(link); err == nil {
		t.Fatal("accepted symlink")
	}
	fifo := filepath.Join(directory, "fifo")
	if err := unix.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readHelperInput(fifo); err == nil {
		t.Fatal("accepted pipe")
	}
}

func TestHelperUninstallRemovesOnlyOwnedArtifacts(t *testing.T) {
	directory := t.TempDir()
	socketDirectory := filepath.Join(directory, "run")
	if err := os.Mkdir(socketDirectory, 0700); err != nil {
		t.Fatal(err)
	}
	paths := []string{filepath.Join(directory, "executable"), filepath.Join(directory, "daemon.plist"), filepath.Join(socketDirectory, "control.sock")}
	for _, path := range paths {
		if err := os.WriteFile(path, []byte("owned"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	unrelated := filepath.Join(directory, "unrelated")
	if err := os.WriteFile(unrelated, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := removeHelperInstallation(paths, socketDirectory); err != nil {
		t.Fatal(err)
	}
	if err := removeHelperInstallation(paths, socketDirectory); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(unrelated); err != nil {
		t.Fatal("removed unrelated file")
	}
}

func TestHelperInstallRollback(t *testing.T) {
	for _, mode := range []string{"success", "stop-error", "bootstrap-error", "first-install-error"} {
		t.Run(mode, func(t *testing.T) {
			directory := t.TempDir()
			files := []helperInstallFile{{path: filepath.Join(directory, "executable"), staged: filepath.Join(directory, "new-executable")}, {path: filepath.Join(directory, "plist"), staged: filepath.Join(directory, "new-plist")}}
			defer cleanupHelperInstallation(files)
			for _, file := range files {
				if mode != "first-install-error" {
					if err := os.WriteFile(file.path, []byte("old"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.WriteFile(file.staged, []byte("new"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			stops, starts := 0, 0
			stop := func() error {
				stops++
				if mode == "stop-error" {
					return errors.New("stop failed")
				}
				return nil
			}
			start := func() error {
				starts++
				if starts == 1 && (mode == "bootstrap-error" || mode == "first-install-error") {
					return errors.New("bootstrap failed")
				}
				return nil
			}
			err := replaceHelperInstallation(files, stop, start)
			if (err == nil) != (mode == "success") {
				t.Fatalf("unexpected error %v", err)
			}
			for _, file := range files {
				data, readErr := os.ReadFile(file.path)
				if mode == "first-install-error" {
					if !os.IsNotExist(readErr) {
						t.Fatal("failed first install left files")
					}
					continue
				}
				expected := "old"
				if mode == "success" {
					expected = "new"
				}
				if readErr != nil || string(data) != expected {
					t.Fatalf("installed state %q %v", data, readErr)
				}
			}
			if mode == "stop-error" && starts != 0 {
				t.Fatal("started after failed stop")
			}
			if mode == "bootstrap-error" && (starts != 2 || stops != 2) {
				t.Fatal("did not restart previous daemon")
			}
		})
	}
}
