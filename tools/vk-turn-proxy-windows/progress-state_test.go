// SPDX-License-Identifier: MIT

package main

import "testing"

func TestProgressStateFromLog(t *testing.T) {
	for _, test := range []struct{ state, line, want string }{
		{"credentials", "TURN relay allocated:", "relay"},
		{"relay", "proxy: first connection up", "handshake"},
		{"credentials", "DEFAULT ROUTE → the tunnel", "connected"},
		{"handshake", "TURN relay allocated:", "handshake"},
		{"connected", "proxy: first connection up", "connected"},
		{"connected", "tunnel: statistics", "connected"},
		{"disconnecting", "DEFAULT ROUTE → the tunnel", "disconnecting"},
		{"failed", "DEFAULT ROUTE → the tunnel", "failed"},
		{"disconnected", "TURN relay allocated:", "disconnected"},
		{"credentials", "unrelated message", "credentials"},
	} {
		if got := progressStateFromLog(test.state, test.line); got != test.want {
			t.Errorf("progressStateFromLog(%q,%q)=%q, want %q", test.state, test.line, got, test.want)
		}
	}
}
