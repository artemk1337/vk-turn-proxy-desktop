// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestLinuxParentHelper(t *testing.T) {
	if os.Getenv("VKTP_LINUX_PARENT_HELPER") == "1" {
		_, _ = io.Copy(io.Discard, os.Stdin)
		os.Exit(0)
	}
}

func linuxParentHelper(t *testing.T) (*exec.Cmd, io.WriteCloser) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestLinuxParentHelper$")
	cmd.Env = append(os.Environ(), "VKTP_LINUX_PARENT_HELPER=1")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stdin.Close()
		cmd.Process.Kill()
	})
	return cmd, stdin
}

func TestLinuxParentExitCancelsTunnel(t *testing.T) {
	for name, watch := range map[string]func(context.Context, int, context.CancelFunc) error{
		"pidfd": watchParent,
		"proc":  watchParentByProc,
	} {
		t.Run(name, func(t *testing.T) {
			cmd, stdin := linuxParentHelper(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if err := watch(ctx, cmd.Process.Pid, cancel); err != nil {
				t.Fatal(err)
			}
			stdin.Close()
			if err := cmd.Wait(); err != nil {
				t.Fatal(err)
			}
			select {
			case <-ctx.Done():
			case <-time.After(3 * time.Second):
				t.Fatal("parent exit did not cancel the tunnel")
			}
		})
	}
}

func TestLinuxCanceledParentWatchDoesNotStopLater(t *testing.T) {
	cmd, stdin := linuxParentHelper(t)
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{}, 1)
	if err := watchParent(ctx, cmd.Process.Pid, func() { stopped <- struct{}{} }); err != nil {
		t.Fatal(err)
	}
	cancel()
	stdin.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stopped:
		t.Fatal("canceled parent watcher called stop")
	case <-time.After(400 * time.Millisecond):
	}
}

func TestLinuxParentWatchRejectsInvalidPID(t *testing.T) {
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

func TestLinuxParentIdentityHandlesSpacesAndParentheses(t *testing.T) {
	// /proc/stat comm is wrapped in parentheses but may itself contain them.
	fields := []string{"S"}
	fields = append(fields, strings.Fields(strings.Repeat("0 ", 18))...)
	fields = append(fields, "12345", "0")
	stat := "72 (UI (network) helper) " + strings.Join(fields, " ")
	if got, err := parseParentIdentity(stat); err != nil || got != "12345" {
		t.Fatalf("identity = %q, %v", got, err)
	}
	for _, state := range []string{"Z", "X"} {
		if _, err := parseParentIdentity(strings.Replace(stat, ") S ", ") "+state+" ", 1)); err == nil {
			t.Fatalf("accepted exited process state %s", state)
		}
	}
}
