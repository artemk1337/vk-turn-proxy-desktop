// SPDX-License-Identifier: MIT
//go:build !windows

package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

func defaultDataDir() (string, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "vk-turn-proxy-desktop"), nil
}

func secureDirectory(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	stat := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || stat.Uid != uint32(os.Getuid()) {
		return errors.New("application directory must belong to the current user")
	}
	return os.Chmod(path, 0700)
}

func readSecret(path string, maxBytes int) ([]byte, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Sys().(*syscall.Stat_t).Uid != uint32(os.Getuid()) {
		return nil, errors.New("private storage must belong to the current user")
	}
	if err := file.Chmod(0600); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(file, int64(maxBytes)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxBytes {
		return nil, fmt.Errorf("private storage exceeds %d bytes", maxBytes)
	}
	return data, nil
}

func writeSecret(path string, data []byte) error { return writePrivate(path, data) }
func replaceFile(from, to string) error          { return os.Rename(from, to) }
func prepareBackendCommand(command *exec.Cmd)    {}
func requireDesktopPrivileges() error            { return nil }
func backendArguments(root string) []string      { return nil }

func prepareMachineDirectory() error { return nil }
