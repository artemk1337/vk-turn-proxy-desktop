// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// A pidfd identifies the original GUI process even if Linux later reuses its PID.
func watchParent(ctx context.Context, pid int, stop context.CancelFunc) error {
	if pid == 0 {
		return nil
	}
	if pid < 0 || uint64(pid) > 1<<31-1 {
		return errors.New("invalid parent process ID")
	}
	fd, err := unix.PidfdOpen(pid, 0)
	if err != nil {
		if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EINVAL) || errors.Is(err, unix.EPERM) {
			return watchParentByProc(ctx, pid, stop)
		}
		return fmt.Errorf("open desktop parent process: %w", err)
	}
	go func() {
		defer unix.Close(fd)
		poll := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		for ctx.Err() == nil {
			_, err := unix.Poll(poll, 250)
			if errors.Is(err, unix.EINTR) {
				continue
			}
			if (err != nil || poll[0].Revents != 0) && ctx.Err() == nil {
				stop()
				return
			}
		}
	}()
	return nil
}

// Older kernels lack pidfds. Compare process start time as well as its PID.
func watchParentByProc(ctx context.Context, pid int, stop context.CancelFunc) error {
	path := fmt.Sprintf("/proc/%d/stat", pid)
	initial, err := readParentIdentity(path)
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
				current, err := readParentIdentity(path)
				if (err != nil || current != initial) && ctx.Err() == nil {
					stop()
					return
				}
			}
		}
	}()
	return nil
}

func readParentIdentity(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return parseParentIdentity(string(data))
}

func parseParentIdentity(stat string) (string, error) {
	end := strings.LastIndex(stat, ") ")
	if end < 0 {
		return "", errors.New("invalid process stat")
	}
	fields := strings.Fields(stat[end+2:])
	if len(fields) < 20 || fields[0] == "Z" || fields[0] == "X" {
		return "", errors.New("parent process has exited")
	}
	if _, err := strconv.ParseUint(fields[19], 10, 64); err != nil {
		return "", errors.New("invalid process start time")
	}
	return fields[19], nil
}
