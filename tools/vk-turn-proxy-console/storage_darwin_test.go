// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDarwinConsoleStorageIsSharedAndPrivate(t *testing.T) {
	oldState, oldCache := defaultStateFile, defaultCredCache
	t.Cleanup(func() { defaultStateFile, defaultCredCache = oldState, oldCache })
	root := t.TempDir()
	defaultStateFile = filepath.Join(root, "state", "network-state.json")
	defaultCredCache = filepath.Join(root, "cache", "credentials.json")
	o := &options{pidFile: defaultPidFile, stateFile: defaultStateFile, credCache: defaultCredCache}
	if err := prepareConsoleStorage(o); err != nil {
		t.Fatal(err)
	}
	for _, directory := range []string{filepath.Dir(defaultStateFile), filepath.Dir(defaultCredCache)} {
		info, err := os.Stat(directory)
		if err != nil || info.Mode().Perm() != 0700 {
			t.Fatalf("private storage %s: %v, %v", directory, info, err)
		}
	}
	if defaultPidFile != "/var/run/vk-turn-proxy-console.pid" {
		t.Fatal("machine-wide lock changed from the existing macOS client")
	}
}

func TestDarwinConsoleStorageRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := secureDarwinDirectory(link); err == nil {
		t.Fatal("accepted symlink as protected storage")
	}
	info, err := os.Stat(target)
	if err != nil || info.Mode().Perm() != 0755 {
		t.Fatal("symlink target was modified", err)
	}
}
