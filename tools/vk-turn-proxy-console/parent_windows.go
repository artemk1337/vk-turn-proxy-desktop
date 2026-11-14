// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"fmt"
	"golang.org/x/sys/windows"
)

// Hold the parent's process handle so PID reuse cannot hide a crashed window.
func watchParent(ctx context.Context, pid int, stop context.CancelFunc) error {
	if pid == 0 {
		return nil
	}
	if pid < 0 || uint64(pid) > 1<<32-1 {
		return fmt.Errorf("invalid parent process ID")
	}
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return fmt.Errorf("open desktop parent process: %w", err)
	}
	go func() {
		defer windows.CloseHandle(handle)
		for ctx.Err() == nil {
			state, err := windows.WaitForSingleObject(handle, 250)
			if err != nil || state == windows.WAIT_OBJECT_0 {
				stop()
				return
			}
		}
	}()
	return nil
}
