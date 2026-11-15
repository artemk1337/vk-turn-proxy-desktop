// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"net"
	"os"
	"runtime"
	"time"
)

func openDesktopLog(path string) (*os.File, error) {
	if runtime.GOOS != "darwin" {
		return nil, errors.New("-desktop-log-socket is supported on macOS")
	}
	connection, err := net.DialTimeout("unix", path, 5*time.Second)
	if err != nil {
		return nil, err
	}
	defer connection.Close()
	return connection.(*net.UnixConn).File()
}

// The GUI reads logs and keeps its socket open until the console has cleaned up.
func watchDesktopLog(ctx context.Context, file *os.File, stop context.CancelFunc) {
	go func() {
		var data [1]byte
		for ctx.Err() == nil {
			if err := file.SetReadDeadline(time.Now().Add(250 * time.Millisecond)); err != nil {
				if ctx.Err() == nil {
					stop()
				}
				return
			}
			_, err := file.Read(data[:])
			if errors.Is(err, os.ErrDeadlineExceeded) {
				continue
			}
			if ctx.Err() == nil {
				stop()
			}
			return
		}
	}()
}
