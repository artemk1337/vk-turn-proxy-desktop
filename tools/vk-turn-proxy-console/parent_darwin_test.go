// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestDarwinParentHelper(t *testing.T) {
	if os.Getenv("VKTP_DARWIN_PARENT_HELPER") == "1" {
		_, _ = io.Copy(io.Discard, os.Stdin)
		os.Exit(0)
	}
}

func darwinParentHelper(t *testing.T) (*exec.Cmd, io.WriteCloser) {
	t.Helper()
	command := exec.Command(os.Args[0], "-test.run=^TestDarwinParentHelper$")
	command.Env = append(os.Environ(), "VKTP_DARWIN_PARENT_HELPER=1")
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		input.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { input.Close(); command.Process.Kill() })
	return command, input
}

func TestDarwinParentExitCancelsTunnel(t *testing.T) {
	command, input := darwinParentHelper(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := watchParent(ctx, command.Process.Pid, cancel); err != nil {
		t.Fatal(err)
	}
	input.Close()
	if err := command.Wait(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("parent exit did not cancel the tunnel")
	}
}

func TestDarwinCanceledParentWatchDoesNotStopLater(t *testing.T) {
	command, input := darwinParentHelper(t)
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{}, 1)
	if err := watchParent(ctx, command.Process.Pid, func() { stopped <- struct{}{} }); err != nil {
		t.Fatal(err)
	}
	cancel()
	input.Close()
	if err := command.Wait(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stopped:
		t.Fatal("canceled parent watcher called stop")
	case <-time.After(400 * time.Millisecond):
	}
}

func TestDarwinParentWatchIdentityAndInvalidPID(t *testing.T) {
	first, err := darwinParentIdentity(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	second, err := darwinParentIdentity(os.Getpid())
	if err != nil || first != second || first <= 0 {
		t.Fatalf("unstable process identity: %v, %v, %v", first, second, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for _, pid := range []int{-1, 1 << 31} {
		if err := watchParent(ctx, pid, cancel); err == nil {
			t.Fatalf("accepted invalid PID %d", pid)
		}
	}
	if err := watchParent(ctx, 0, cancel); err != nil {
		t.Fatal(err)
	}
}
