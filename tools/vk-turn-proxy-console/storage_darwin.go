// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

const defaultPidFile = "/var/run/vk-turn-proxy-console.pid"

var defaultStateFile = "/var/db/vk-turn-proxy-desktop/network-state.json"
var defaultCredCache = "/var/db/vk-turn-proxy-desktop/credentials.json"

// GUI and terminal clients share root-owned recovery state regardless of cwd.
func prepareConsoleStorage(o *options) error {
	for _, item := range []struct{ path, defaultPath string }{
		{o.stateFile, defaultStateFile},
		{o.credCache, defaultCredCache},
	} {
		if filepath.Clean(item.path) == item.defaultPath {
			if err := secureDarwinDirectory(filepath.Dir(item.defaultPath)); err != nil {
				return err
			}
		}
	}
	return nil
}

func secureDarwinDirectory(path string) error {
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
