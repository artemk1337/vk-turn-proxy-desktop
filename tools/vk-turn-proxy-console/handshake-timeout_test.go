// SPDX-License-Identifier: MIT

package main

import (
	"testing"
	"time"
)

func TestHandshakeTimeoutFlags(t *testing.T) {
	defaults, err := parseFlags(nil)
	if err != nil {
		t.Fatal(err)
	}
	if defaults.srtpHandshakeTimeout != 10*time.Second || defaults.dtlsHandshakeTimeout != 15*time.Second || defaults.wrapAHandshakeTimeout != 10*time.Second {
		t.Fatalf("unexpected defaults: %+v", defaults)
	}
	for _, validation := range []bool{false, true} {
		args := []string{"-srtp-handshake-timeout", "60s", "-dtls-handshake-timeout", "1s", "-wrapa-handshake-timeout", "300s"}
		if validation {
			args = append(args, "-validate-profile")
		}
		options, err := parseFlags(args)
		if err != nil {
			t.Fatal(err)
		}
		if options.srtpHandshakeTimeout != time.Minute || options.dtlsHandshakeTimeout != time.Second || options.wrapAHandshakeTimeout != 5*time.Minute {
			t.Fatalf("timeouts were not parsed: %+v", options)
		}
		for _, flag := range []string{"-srtp-handshake-timeout", "-dtls-handshake-timeout", "-wrapa-handshake-timeout"} {
			for _, value := range []string{"0s", "-1s", "999ms", "301s", "invalid"} {
				args := []string{flag, value}
				if validation {
					args = append(args, "-validate-profile")
				}
				if _, err := parseFlags(args); err == nil {
					t.Fatalf("accepted %v", args)
				}
			}
		}
	}
}
