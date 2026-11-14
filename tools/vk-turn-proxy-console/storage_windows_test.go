package main

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestConsoleStorageProtectsDefaultDirectories(t *testing.T) {
	root := t.TempDir()
	oldPID, oldState, oldCache := defaultPidFile, defaultStateFile, defaultCredCache
	t.Cleanup(func() { defaultPidFile, defaultStateFile, defaultCredCache = oldPID, oldState, oldCache })
	defaultPidFile = filepath.Join(root, "machine", "client.pid")
	defaultStateFile = filepath.Join(root, "machine", "network-state.json")
	defaultCredCache = filepath.Join(root, "user", "credentials.json")
	o := &options{pidFile: defaultPidFile, stateFile: defaultStateFile, credCache: defaultCredCache}
	if err := prepareConsoleStorage(o); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Dir(defaultPidFile), filepath.Dir(defaultCredCache)} {
		security, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
		if err != nil {
			t.Fatal(err)
		}
		control, _, err := security.Control()
		if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
			t.Fatal("application data directory inherits a public ACL")
		}
	}
}

func TestConsoleStorageKeepsCustomPathsUntouched(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "not-created", "custom")
	o := &options{pidFile: path + ".pid", stateFile: path + ".json", credCache: path + ".cache"}
	if err := prepareConsoleStorage(o); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatal("preparing default storage modified a custom directory")
	}
}
