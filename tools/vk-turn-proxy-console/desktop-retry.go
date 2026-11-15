// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"regexp"
	"time"
)

var turnRetryCode = regexp.MustCompile(`\b(401|403|437|438|486|500|508)\b`)

// Send only a status code or error category. Provider errors can contain
// private URLs and credentials, which must not reach presentation state.
func printDesktopRetry(output io.Writer, err error, delay time.Duration) {
	event := struct {
		Error        string `json:"error"`
		RetryAfterMs int64  `json:"retryAfterMs"`
	}{RetryAfterMs: delay.Milliseconds()}
	if err != nil {
		event.Error = turnRetryCode.FindString(err.Error())
		if event.Error == "" {
			event.Error = "connection"
			var networkError net.Error
			if errors.As(err, &networkError) {
				event.Error = "network"
			}
		}
	}
	data, _ := json.Marshal(event)
	// One write keeps concurrent telemetry records from interleaving.
	_, _ = output.Write(append(append([]byte("desktop-retry "), data...), '\n'))
}
