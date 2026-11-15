// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"strings"
	"time"
)

const retryPrefix = "desktop-retry "

// applyRetry runs under the app lock. Retry telemetry never enters the log.
func (a *App) applyRetry(line string, now time.Time) {
	if a.connection == "disconnecting" || connectionRank(a.connection) == 0 {
		return
	}
	var event struct {
		Error        string `json:"error"`
		RetryAfterMs int64  `json:"retryAfterMs"`
	}
	if len(line) > 2048 || json.Unmarshal([]byte(strings.TrimPrefix(line, retryPrefix)), &event) != nil || event.RetryAfterMs < 0 || event.RetryAfterMs > 60000 {
		return
	}
	switch event.Error {
	case "", "401", "403", "437", "438", "486", "500", "508", "network", "connection":
	default:
		event.Error = "connection"
	}
	a.retryError, a.retryAt = event.Error, 0
	if event.Error != "" {
		a.cancelSpeedTestLocked()
		a.retryAt = now.Add(time.Duration(event.RetryAfterMs) * time.Millisecond).UnixMilli()
		a.traffic = trafficSample{}
	}
}
