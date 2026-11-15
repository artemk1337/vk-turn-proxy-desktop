// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"os"

	"github.com/artemk1337/vk-turn-proxy-desktop/pkg/proxy"
)

// Keep machine telemetry out of the human log and its disk file.
func printDesktopTraffic(stats proxy.Stats) {
	fmt.Fprintf(os.Stdout, "desktop-traffic %d %d\n", stats.TxBytes, stats.RxBytes)
}
