// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"golang.org/x/sys/unix"
)

// Compare the original process start time so PID reuse cannot keep a tunnel alive.
func watchParent(ctx context.Context, pid int, stop context.CancelFunc) error {
	if pid == 0 {
		return nil
	}
	if pid < 0 || uint64(pid) > 1<<31-1 {
		return errors.New("invalid parent process ID")
	}
	initial, err := darwinParentIdentity(pid)
	if err != nil {
		return fmt.Errorf("read desktop parent process: %w", err)
	}
	go func() {
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				current, err := darwinParentIdentity(pid)
				if (err != nil || current != initial) && ctx.Err() == nil {
					stop()
					return
				}
			}
		}
	}()
	return nil
}

func darwinParentIdentity(pid int) (int64, error) {
	process, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return 0, err
	}
	if process.Proc.P_pid != int32(pid) || process.Proc.P_stat == 5 { // SZOMB
		return 0, errors.New("parent process has exited")
	}
	return process.Proc.P_starttime.Nano(), nil
}
