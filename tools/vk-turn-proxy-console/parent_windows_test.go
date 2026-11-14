//go:build windows

package main

import (
	"context"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestWindowsParentHelper(t *testing.T) {
	if os.Getenv("VKTP_PARENT_HELPER") != "1" {
		return
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
	os.Exit(0)
}

func startWindowsParentHelper(t *testing.T) (*exec.Cmd, io.WriteCloser) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestWindowsParentHelper$")
	cmd.Env = append(os.Environ(), "VKTP_PARENT_HELPER=1")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
	})
	return cmd, stdin
}

func TestWindowsParentExitCancelsTunnel(t *testing.T) {
	cmd, stdin := startWindowsParentHelper(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := watchParent(ctx, cmd.Process.Pid, cancel); err != nil {
		t.Fatal(err)
	}
	_ = stdin.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the tunnel was not canceled after its desktop parent exited")
	}
}

func TestWindowsCanceledParentWatchStopsWaiting(t *testing.T) {
	cmd, stdin := startWindowsParentHelper(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stopped := make(chan struct{}, 1)
	if err := watchParent(ctx, cmd.Process.Pid, func() { stopped <- struct{}{} }); err != nil {
		t.Fatal(err)
	}
	cancel()
	// The process handle wait polls every 250 ms. Let it observe cancellation
	// while the helper is still alive, then verify its later exit has no effect.
	time.Sleep(500 * time.Millisecond)
	_ = stdin.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stopped:
		t.Fatal("a canceled watcher reacted to the later parent exit")
	case <-time.After(500 * time.Millisecond):
	}
}

func TestWindowsParentWatchRejectsInvalidPID(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for _, pid := range []int{-1, int(^uint32(0))} {
		if err := watchParent(ctx, pid, cancel); err == nil {
			t.Fatalf("invalid parent PID %d accepted", pid)
		}
	}
	if err := watchParent(ctx, 0, cancel); err != nil {
		t.Fatal("zero means a standalone console", err)
	}
}
