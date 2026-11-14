// SPDX-License-Identifier: MIT

package main

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"unicode/utf16"
)

// PowerShell receives encoded scripts so interface names remain data.
func powershell(script string) []string {
	units := utf16.Encode([]rune("$ErrorActionPreference='Stop'; [Console]::OutputEncoding=[System.Text.UTF8Encoding]::new($false); " + script))
	b := make([]byte, len(units)*2)
	for i, unit := range units {
		binary.LittleEndian.PutUint16(b[i*2:], unit)
	}
	return []string{"powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-EncodedCommand", base64.StdEncoding.EncodeToString(b)}
}

func psQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }

const windowsRouteMetric = 42757

type windowsCmds struct{}

func (windowsCmds) addrUp(ifname, cidr string, mtu int) [][]string {
	ip, subnet, _ := net.ParseCIDR(cidr)
	prefix, _ := subnet.Mask.Size()
	alias := psQuote(ifname)
	return [][]string{powershell(fmt.Sprintf("New-NetIPAddress -InterfaceAlias %s -IPAddress %s -PrefixLength %d -AddressFamily IPv4 | Out-Null; Set-NetIPInterface -InterfaceAlias %s -AddressFamily IPv4 -NlMtuBytes %d -AutomaticMetric Disabled -InterfaceMetric 1", alias, psQuote(ip.String()), prefix, alias, mtu))}
}

func (windowsCmds) subnetRoute(string, string) (add, del []string) { return nil, nil }

func windowsRoute(prefix, iface, hop string) (add, del []string) {
	filter := fmt.Sprintf("Get-NetRoute -DestinationPrefix %s -PolicyStore ActiveStore -ErrorAction SilentlyContinue", psQuote(prefix))
	// A distinct metric lets crash recovery remove only routes created here.
	add = powershell(fmt.Sprintf("if (@(%s).Count -gt 0) { throw 'File exists' }; New-NetRoute -DestinationPrefix %s -InterfaceAlias %s -NextHop %s -RouteMetric %d -PolicyStore ActiveStore | Out-Null", filter, psQuote(prefix), psQuote(iface), psQuote(hop), windowsRouteMetric))
	del = powershell(fmt.Sprintf("%s | Where-Object { $_.RouteMetric -eq %d } | Remove-NetRoute -Confirm:$false", filter, windowsRouteMetric))
	return add, del
}

func (windowsCmds) splitRoutes(ifname string) []routeChange {
	var out []routeChange
	for _, prefix := range []string{"0.0.0.0/1", "128.0.0.0/1"} {
		add, del := windowsRoute(prefix, ifname, "0.0.0.0")
		out = append(out, routeChange{key: prefix, add: add, del: del})
	}
	return out
}

func (windowsCmds) ipv6Block(ifname string) []routeChange {
	var out []routeChange
	for _, prefix := range []string{"::/1", "8000::/1"} {
		add, del := windowsRoute(prefix, ifname, "::")
		out = append(out, routeChange{key: prefix, add: add, del: del})
	}
	return out
}

func (windowsCmds) viaTunnel(ifname, ip string) (add, del []string) {
	return windowsRoute(ip+"/32", ifname, "0.0.0.0")
}

func (windowsCmds) pin(ip string, gw gateway) (add, del []string) {
	hop := gw.IP
	if hop == "" {
		hop = "0.0.0.0"
	}
	return windowsRoute(ip+"/32", gw.Iface, hop)
}

func (windowsCmds) repin(ip string, gw gateway) []string {
	hop := gw.IP
	if hop == "" {
		hop = "0.0.0.0"
	}
	// Both the old and new interface are covered by the journal's metric filter.
	return powershell(fmt.Sprintf("Get-NetRoute -DestinationPrefix %s -PolicyStore ActiveStore -ErrorAction SilentlyContinue | Where-Object { $_.RouteMetric -eq %d } | Remove-NetRoute -Confirm:$false; New-NetRoute -DestinationPrefix %s -InterfaceAlias %s -NextHop %s -RouteMetric %d -PolicyStore ActiveStore | Out-Null", psQuote(ip+"/32"), windowsRouteMetric, psQuote(ip+"/32"), psQuote(gw.Iface), psQuote(hop), windowsRouteMetric))
}

func (windowsCmds) hostRouteQuery(string) []string { return nil }
func (windowsCmds) destroy(string) []string        { return nil }

func parseWindowsDefaultRoute(out string) (gateway, bool) {
	var route struct {
		NextHop        string
		InterfaceAlias string
	}
	if json.Unmarshal([]byte(strings.TrimSpace(out)), &route) != nil || net.ParseIP(route.NextHop) == nil || route.InterfaceAlias == "" {
		return gateway{}, false
	}
	return gateway{IP: route.NextHop, Iface: route.InterfaceAlias}, true
}

func windowsDefaultRouteCommand() []string {
	return powershell("Get-NetRoute -AddressFamily IPv4 -DestinationPrefix '0.0.0.0/0' -PolicyStore ActiveStore | Where-Object { $_.State -eq 'Alive' } | Sort-Object @{Expression={$_.RouteMetric + $_.InterfaceMetric}} | Select-Object -First 1 NextHop,InterfaceAlias | ConvertTo-Json -Compress")
}
