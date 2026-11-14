// SPDX-License-Identifier: MIT

//go:build windows

package main

import (
	"fmt"
	"golang.zx2c4.com/wireguard/tun"
)

var hostCmds netCmds = windowsCmds{}

const defaultTunName = "VKTurnProxy"

func openTUN(name string, mtu int) (tun.Device, string, error) {
	dev, err := tun.CreateTUN(name, mtu)
	if err != nil {
		return nil, "", fmt.Errorf("create %s (place wintun.dll beside the executable): %w", name, err)
	}
	real, err := dev.Name()
	if err != nil {
		_ = dev.Close()
		return nil, "", err
	}
	return dev, real, nil
}

func readDefaultRoute() (gateway, bool) {
	out, err := runCmd(windowsDefaultRouteCommand())
	if err != nil {
		return gateway{}, false
	}
	return parseWindowsDefaultRoute(out)
}

func sshPeers() []string { return nil }

func newSystemDNS(j *journal, run func([]string) (string, error), _ func(string, ...any)) systemDNS {
	d := &windowsDNS{j: j, run: run}
	if gw, ok := readDefaultRoute(); ok {
		d.refresh(gw)
	}
	return d
}
