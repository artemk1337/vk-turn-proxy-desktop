package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPreferencesDefaultsAndRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preferences.json")
	got, err := loadPreferences(path, "ru")
	if err != nil || got.Language != "ru" || got.Theme != "light" || got.AutoStart {
		t.Fatalf("defaults: %#v %v", got, err)
	}
	want := preferences{Language: "en", Theme: "dark", AutoStart: true}
	if err := savePreferences(path, want); err != nil {
		t.Fatal(err)
	}
	got, err = loadPreferences(path, "ru")
	if err != nil || got != want {
		t.Fatalf("round trip: %#v %v", got, err)
	}
}
func TestPreferencesRejectInvalidValuesAndMalformedJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preferences.json")
	if err := os.WriteFile(path, []byte(`{"language":"xx","theme":"blue"}`), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := loadPreferences(path, "ru")
	if err != nil || got.Language != "ru" || got.Theme != "light" {
		t.Fatal(got, err)
	}
	if err := os.WriteFile(path, []byte(`{`), 0600); err != nil {
		t.Fatal(err)
	}
	got, err = loadPreferences(path, "en")
	if err == nil || got != defaultPreferences("en") {
		t.Fatal(got, err)
	}
}
