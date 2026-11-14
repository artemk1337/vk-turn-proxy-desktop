// SPDX-License-Identifier: MIT

//go:build unix

package main

import (
	"os"
	"syscall"
)

func restoreLogOwner(file *os.File, previous os.FileInfo) error {
	old := previous.Sys().(*syscall.Stat_t)
	info, err := file.Stat()
	if err != nil {
		return err
	}
	current := info.Sys().(*syscall.Stat_t)
	if current.Uid == old.Uid && current.Gid == old.Gid {
		return nil
	}
	return file.Chown(int(old.Uid), int(old.Gid))
}
