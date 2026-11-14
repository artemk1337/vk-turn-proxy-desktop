// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestEncryptedProfileStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profiles.dat")
	if got, err := loadProfileStore(path); err != nil || len(got.Profiles) != 0 {
		t.Fatalf("missing store=%v, %v", got, err)
	}
	expected := profileStore{Profiles: []savedProfile{{ID: "one", Name: "Home", Mode: "link", ProfileText: "private-profile-secret"}}, ActiveID: "one"}
	for range 2 {
		if err := saveProfileStore(path, expected); err != nil {
			t.Fatal(err)
		}
		encrypted, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(encrypted, []byte("private-profile-secret")) {
			t.Fatal("profile written in plaintext")
		}
		got, err := loadProfileStore(path)
		if err != nil || !reflect.DeepEqual(got, expected) {
			t.Fatalf("round trip=%v, %v", got, err)
		}
	}
	if err := os.WriteFile(path, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadProfileStore(path); err == nil {
		t.Fatal("corrupt store accepted")
	}
}
