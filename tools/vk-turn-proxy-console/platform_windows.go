// SPDX-License-Identifier: MIT

//go:build windows

package main

import (
	"fmt"
	"log"
	"strings"
	"time"

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
	command := windowsDefaultRouteCommand()
	out, err := runCmd(command)
	if err != nil {
		cause := strings.TrimPrefix(err.Error(), strings.Join(command, " ")+": ")
		issue := fmt.Sprintf("PowerShell Get-NetRoute query failed: %q; output=%q", windowsRouteDiagnosticText(cause), windowsRouteDiagnosticText(out))
		if message := windowsGatewayQueryDiagnostics.message(issue, time.Now()); message != "" {
			log.Print(message)
		}
		return gateway{}, false
	}
	gw, err := parseWindowsDefaultRouteDetail(out)
	issue := ""
	if err != nil {
		issue = fmt.Sprintf("%s; output=%q", err, windowsRouteDiagnosticText(out))
	}
	if message := windowsGatewayQueryDiagnostics.message(issue, time.Now()); message != "" {
		log.Print(message)
	}
	return gw, err == nil
}

func sshPeers() []string { return nil }

func newSystemDNS(j *journal, run func([]string) (string, error), _ func(string, ...any)) systemDNS {
	d := &windowsDNS{j: j, run: run}
	if gw, ok := readDefaultRoute(); ok {
		d.refresh(gw)
	}
	return d
}
