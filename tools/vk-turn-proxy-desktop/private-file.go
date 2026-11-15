// SPDX-License-Identifier: MIT
package main

import (
	"os"
	"path/filepath"
)

// Session files remain plaintext so the privileged console can read them.
func writePrivate(path string, data []byte) error {
	directory := filepath.Dir(path)
	if err := secureDirectory(directory); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return &os.PathError{Op: "write", Path: path, Err: os.ErrPermission}
	}
	file, err := os.CreateTemp(directory, ".private-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return replaceFile(file.Name(), path)
}
