// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLinuxConsoleStorageIsSharedAndPrivate(t *testing.T) {
	oldPID, oldState, oldCache := defaultPidFile, defaultStateFile, defaultCredCache
	t.Cleanup(func() { defaultPidFile, defaultStateFile, defaultCredCache = oldPID, oldState, oldCache })
	root := t.TempDir()
	defaultPidFile = filepath.Join(root, "run", "client.pid")
	defaultStateFile = filepath.Join(root, "state", "network-state.json")
	defaultCredCache = filepath.Join(root, "state", "credentials.json")
	o := &options{pidFile: defaultPidFile, stateFile: defaultStateFile, credCache: defaultCredCache}
	if err := prepareConsoleStorage(o); err != nil {
		t.Fatal(err)
	}
	for _, directory := range []string{filepath.Dir(defaultPidFile), filepath.Dir(defaultStateFile)} {
		info, err := os.Stat(directory)
		if err != nil || info.Mode().Perm() != 0700 {
			t.Fatalf("private storage %s: %v, %v", directory, info, err)
		}
	}
}

func TestLinuxConsoleStorageRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := secureLinuxDirectory(link); err == nil {
		t.Fatal("accepted symlink as protected storage")
	}
	info, err := os.Stat(target)
	if err != nil || info.Mode().Perm() != 0755 {
		t.Fatal("symlink target was modified", err)
	}
}
