// SPDX-License-Identifier: MIT

//go:build windows

package main

import (
	"fmt"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"strconv"
)

type pidFile struct{ f *os.File }

func takePidFile(path string) (*pidFile, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE, windows.FILE_SHARE_READ, nil, windows.OPEN_ALWAYS, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, fmt.Errorf("pid file %s: another client may be running: %w", path, err)
	}
	f := os.NewFile(uintptr(handle), path)
	if err := f.Truncate(0); err != nil {
		f.Close()
		return nil, err
	}
	if _, err := f.WriteString(strconv.Itoa(os.Getpid()) + "\n"); err != nil {
		f.Close()
		return nil, err
	}
	return &pidFile{f: f}, nil
}

// The file remains on disk, while Windows releases its exclusive write handle
// on every process exit. Keeping the path prevents a close/remove race.
func (p *pidFile) release() { _ = p.f.Close() }
