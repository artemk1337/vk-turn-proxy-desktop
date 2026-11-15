//go:build !windows

// SPDX-License-Identifier: MIT
//
package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestPrivateStorageRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "profiles.json")
	value := []byte(`{"profiles":[]}`)
	if err := writeSecret(path, value); err != nil {
		t.Fatal(err)
	}
	if err := writeSecret(path, append(value, '\n')); err != nil {
		t.Fatal(err)
	}
	data, err := readSecret(path, 1024)
	if err != nil || !bytes.Equal(data, append(value, '\n')) {
		t.Fatalf("readSecret = %q, %v", data, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("file mode = %o", info.Mode().Perm())
	}
	info, err = os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0700 {
		t.Fatalf("directory mode = %o", info.Mode().Perm())
	}
	if _, err := readSecret(path, 1); err == nil {
		t.Fatal("oversized secret accepted")
	}
}

func TestPrivateStorageRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := writePrivate(link, []byte("replacement")); err == nil {
		t.Fatal("write followed symlink")
	}
	if _, err := readSecret(link, 1024); err == nil {
		t.Fatal("read followed symlink")
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "original" {
		t.Fatalf("target changed = %q, %v", data, err)
	}
	if err := secureDirectory(link); err == nil {
		t.Fatal("symlink directory accepted")
	}
}
