// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestVKAuthStoredEncryptedAndCleared(t *testing.T) {
	path := filepath.Join(t.TempDir(), "account", "vk-auth.bin")
	text := "remixsid=fixture-private-session; p=fixture-private-token"
	if err := storeVKAuth(path, text); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("fixture-private")) {
		t.Fatal("session was stored as plaintext")
	}
	got, err := loadVKAuth(path)
	if err != nil || got != text {
		t.Fatalf("session round trip failed: %v", err)
	}
	if err := storeVKAuth(path, ""); err != nil {
		t.Fatal(err)
	}
	got, err = loadVKAuth(path)
	if err != nil || got != "" {
		t.Fatal("session was not cleared")
	}
}

func TestVKAuthCorruptStorageReturnsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vk-auth.bin")
	if err := os.WriteFile(path, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadVKAuth(path); err == nil {
		t.Fatal("corrupt storage was accepted")
	}
}
