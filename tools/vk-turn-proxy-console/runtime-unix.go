// SPDX-License-Identifier: MIT

//go:build !windows

package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
)

// defaultPidFile: /var/run is there, root's and emptied at boot on macOS,
// Linux (where it is /run) and FreeBSD alike. The state file is NOT kept
// there: it must outlive a reboot — a DNS setting it has to put back does.
const defaultPidFile = "/var/run/vk-turn-proxy-console.pid"

const defaultStateFile = ".vk-turn-proxy-console-state.json"
const defaultCredCache = ".vk-turn-proxy-console-creds.json"

func prepareConsoleStorage(*options) error { return nil }

// ignoreBrokenPipe: a dead log must not kill a process that owes a cleanup.
// A write to a closed pipe on stdout or stderr ends a Go program with SIGPIPE
// unless the signal is ignored — and that is what Ctrl-C does to
// `console 2>&1 | tee file`: the terminal signals the whole foreground
// group, tee dies of it, and the console's next log line is a write to a
// broken pipe. It died in the middle of its shutdown, the DNS and the split
// routes taken back and the pins left — pointing at the gateway of a network
// the machine then left, with that network's own DNS servers among them (the
// field, 2026-09-30; reproduced on a stand: exit 141). With the signal
// ignored the write fails, the log package drops the line, and the shutdown
// goes on to its end.
func ignoreBrokenPipe() {
	signal.Ignore(syscall.SIGPIPE)
}

func requireRoot() error {
	if os.Geteuid() != 0 {
		return errors.New("run it as root (sudo): the console creates a tunnel interface and changes the routing table and the DNS")
	}
	return nil
}

func hideCommandWindow(*exec.Cmd) {}

func watchParent(_ context.Context, pid int, _ context.CancelFunc) error {
	if pid != 0 {
		return errors.New("-parent-pid is supported on Windows")
	}
	return nil
}
