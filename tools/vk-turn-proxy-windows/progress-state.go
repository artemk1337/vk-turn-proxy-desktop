// SPDX-License-Identifier: MIT

package main

func connectionStage(state string) int {
	switch state {
	case "credentials":
		return 0
	case "relay":
		return 1
	case "handshake":
		return 2
	case "connected":
		return 3
	default:
		return -1
	}
}

func progressStateFromLog(state, line string) string {
	current := connectionStage(state)
	if current < 0 {
		return state
	}
	rank, _ := logPhase(line)
	if rank <= current {
		return state
	}
	return []string{"credentials", "relay", "handshake", "connected"}[rank]
}
