// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"os"
	"time"
)

// watchStopFile lets the desktop window request the normal journaled shutdown.
func watchStopFile(ctx context.Context, path string, stop context.CancelFunc, logf func(string, ...any)) {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, err := os.Stat(path)
			if err == nil {
				logf("shutdown requested by desktop window")
				stop()
				return
			}
			if !errors.Is(err, os.ErrNotExist) {
				logf("stop file: %v", err)
				stop()
				return
			}
		}
	}
}
