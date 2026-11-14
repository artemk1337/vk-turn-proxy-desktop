// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

var defaultPidFile = "/run/vk-turn-proxy-desktop/client.pid"
var defaultStateFile = "/var/lib/vk-turn-proxy-desktop/network-state.json"
var defaultCredCache = "/var/lib/vk-turn-proxy-desktop/credentials.json"

// Linux GUI and terminal clients share protected machine state regardless of cwd.
func prepareConsoleStorage(o *options) error {
	for _, item := range []struct{ path, defaultPath string }{
		{o.pidFile, defaultPidFile},
		{o.stateFile, defaultStateFile},
		{o.credCache, defaultCredCache},
	} {
		if filepath.Clean(item.path) == item.defaultPath {
			if err := secureLinuxDirectory(filepath.Dir(item.defaultPath)); err != nil {
				return err
			}
		}
	}
	return nil
}

func secureLinuxDirectory(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return fmt.Errorf("create console storage: %w", err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("console storage must be a directory owned by the current user: %s", path)
	}
	return os.Chmod(path, 0700)
}
