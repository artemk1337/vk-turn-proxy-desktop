// SPDX-License-Identifier: MIT

//go:build !windows && !linux

package main

import (
	"context"
	"errors"
)

const defaultPidFile = "/var/run/vk-turn-proxy-console.pid"
const defaultStateFile = ".vk-turn-proxy-console-state.json"
const defaultCredCache = ".vk-turn-proxy-console-creds.json"

func prepareConsoleStorage(*options) error { return nil }

func watchParent(_ context.Context, pid int, _ context.CancelFunc) error {
	if pid != 0 {
		return errors.New("-parent-pid is supported on Windows and Linux")
	}
	return nil
}
