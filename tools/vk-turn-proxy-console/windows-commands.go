// SPDX-License-Identifier: MIT

package main

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"slices"
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
	del = powershell(windowsRouteCleanup(prefix) + "; exit 0")
	return add, del
}

// Enumerate the table without a destination lookup so an absent route is success.
// Verify after each attempt, including when an interface disappears during removal.
func windowsRouteCleanup(prefix string) string {
	query := fmt.Sprintf("Get-NetRoute -PolicyStore ActiveStore -ErrorAction Stop | Where-Object { $_.DestinationPrefix -eq %s -and $_.RouteMetric -eq %d }", psQuote(prefix), windowsRouteMetric)
	return fmt.Sprintf(`$remaining = @(%s); for ($attempt = 0; $attempt -lt 3 -and $remaining.Count -gt 0; $attempt++) {
 $removeError = $null
 try { $remaining | Remove-NetRoute -Confirm:$false -ErrorAction Stop } catch { $removeError = $_ }
 $remaining = @(%s)
 if ($remaining.Count -gt 0) { Start-Sleep -Milliseconds 200 }
}; if ($remaining.Count -gt 0) {
 if ($null -ne $removeError) { throw $removeError }
 throw %s
}`, query, query, psQuote("Client route still exists after cleanup: "+prefix))
}

// Upgrade only the exact route undo commands written by earlier releases.
func windowsRouteUndo(s undoStep) []string {
	prefix := ""
	switch {
	case strings.HasPrefix(s.Key, "route split "):
		prefix = strings.TrimPrefix(s.Key, "route split ")
	case strings.HasPrefix(s.Key, "route ipv6 "):
		prefix = strings.TrimPrefix(s.Key, "route ipv6 ")
	case strings.HasPrefix(s.Key, "route via "):
		prefix = strings.TrimPrefix(s.Key, "route via ") + "/32"
	case strings.HasPrefix(s.Key, "pin "):
		prefix = strings.TrimPrefix(s.Key, "pin ") + "/32"
	default:
		return s.Argv
	}
	if _, _, err := net.ParseCIDR(prefix); err != nil {
		return s.Argv
	}
	legacy := powershell(fmt.Sprintf("Get-NetRoute -DestinationPrefix %s -PolicyStore ActiveStore -ErrorAction SilentlyContinue | Where-Object { $_.RouteMetric -eq %d } | Remove-NetRoute -Confirm:$false", psQuote(prefix), windowsRouteMetric))
	if slices.Equal(s.Argv, legacy) {
		return powershell(windowsRouteCleanup(prefix) + "; exit 0")
	}
	return s.Argv
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
	return powershell(windowsRouteCleanup(ip+"/32") + fmt.Sprintf("; New-NetRoute -DestinationPrefix %s -InterfaceAlias %s -NextHop %s -RouteMetric %d -PolicyStore ActiveStore | Out-Null", psQuote(ip+"/32"), psQuote(gw.Iface), psQuote(hop), windowsRouteMetric))
}

func (windowsCmds) hostRouteQuery(string) []string { return nil }
func (windowsCmds) destroy(string) []string        { return nil }

func parseWindowsDefaultRoute(out string) (gateway, bool) {
	gw, err := parseWindowsDefaultRouteDetail(out)
	return gw, err == nil
}

func parseWindowsDefaultRouteDetail(out string) (gateway, error) {
	out = strings.TrimSpace(out)
	if out == "" || out == "null" {
		return gateway{}, fmt.Errorf("Get-NetRoute returned no IPv4 default route matching State=Alive")
	}
	var route struct {
		NextHop        string
		InterfaceAlias string
	}
	if err := json.Unmarshal([]byte(out), &route); err != nil {
		return gateway{}, fmt.Errorf("cannot parse Get-NetRoute JSON: %w", err)
	}
	if net.ParseIP(route.NextHop) == nil {
		return gateway{}, fmt.Errorf("Get-NetRoute returned invalid NextHop %q", windowsRouteDiagnosticText(route.NextHop))
	}
	if route.InterfaceAlias == "" {
		return gateway{}, fmt.Errorf("Get-NetRoute returned an empty InterfaceAlias")
	}
	return gateway{IP: route.NextHop, Iface: route.InterfaceAlias}, nil
}

func windowsDefaultRouteCommand() []string {
	// First-use module progress is serialized as CLIXML and would corrupt the JSON.
	return powershell("$ProgressPreference='SilentlyContinue'; Get-NetRoute -AddressFamily IPv4 -DestinationPrefix '0.0.0.0/0' -PolicyStore ActiveStore | Where-Object { $_.State -eq 'Alive' } | Sort-Object @{Expression={$_.RouteMetric + $_.InterfaceMetric}} | Select-Object -First 1 NextHop,InterfaceAlias | ConvertTo-Json -Compress")
}
