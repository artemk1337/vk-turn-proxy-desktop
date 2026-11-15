// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"net/netip"
	"strings"
)

// A single durable undo covers the whole plan, including a partial batch.
// Route removal is scoped to the tunnel or the client's unique metric.
type policyRouteBatch struct {
	Tunnel   string   `json:"tunnel"`
	Prefixes []string `json:"prefixes"`
}

const policyRouteMetric = 42757

func policyRoute(cmds netCmds, tunnel, prefix string) routeChange {
	var add, del []string
	switch cmds.(type) {
	case windowsCmds:
		add, del = windowsRoute(prefix, tunnel, "0.0.0.0")
	case linuxCmds:
		add = []string{"ip", "-4", "route", "add", prefix, "dev", tunnel, "metric", fmt.Sprint(policyRouteMetric)}
		del = []string{"ip", "-4", "route", "del", prefix, "dev", tunnel, "metric", fmt.Sprint(policyRouteMetric)}
	default:
		add = []string{"route", "-q", "-n", "add", "-inet", prefix, "-interface", tunnel}
		del = []string{"route", "-q", "-n", "delete", "-inet", prefix, "-interface", tunnel}
	}
	return routeChange{key: prefix, add: add, del: del}
}

func shellQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }

func policyBatchCommand(batch policyRouteBatch, add bool, cmds netCmds) []string {
	if _, ok := cmds.(windowsCmds); ok {
		prefixes := make([]string, 0, len(batch.Prefixes))
		for _, prefix := range batch.Prefixes {
			prefixes = append(prefixes, psQuote(prefix))
		}
		list := "@(" + strings.Join(prefixes, ",") + ")"
		if add {
			return powershell("$existing = @{}; Get-NetRoute -PolicyStore ActiveStore -ErrorAction Stop | ForEach-Object { $existing[$_.DestinationPrefix] = $true }; foreach ($prefix in " + list + ") { if ($existing.ContainsKey($prefix)) { throw 'File exists' }; New-NetRoute -DestinationPrefix $prefix -InterfaceAlias " + psQuote(batch.Tunnel) + " -NextHop '0.0.0.0' -RouteMetric 42757 -PolicyStore ActiveStore | Out-Null }")
		}
		query := "Get-NetRoute -PolicyStore ActiveStore -ErrorAction Stop | Where-Object { $_.InterfaceAlias -eq " + psQuote(batch.Tunnel) + " -and $_.RouteMetric -eq 42757 -and $prefixes -contains $_.DestinationPrefix }"
		return powershell("$prefixes = " + list + "; $routes = @(" + query + "); if ($routes.Count -gt 0) { $routes | Remove-NetRoute -Confirm:$false -ErrorAction Stop }; if (@(" + query + ").Count -gt 0) { throw 'Client policy routes still exist after cleanup' }")
	}
	var script strings.Builder
	if !add {
		script.WriteString("status=0\n")
	}
	for _, prefix := range batch.Prefixes {
		route := policyRoute(cmds, batch.Tunnel, prefix)
		argv := route.del
		if add {
			argv = route.add
		} else {
			script.WriteString("output=$(")
		}
		for index, arg := range argv {
			if index > 0 {
				script.WriteByte(' ')
			}
			script.WriteString(shellQuote(arg))
		}
		if add {
			script.WriteString(" || exit $?\n")
		} else {
			script.WriteString(" 2>&1) || { case \"$output\" in *'not in table'*|*'route has not been found'*|*'No such process'*|*'Cannot find device'*|*'bad address: utun'*) ;; *) printf '%s\\n' \"$output\" >&2; status=1 ;; esac; }\n")
		}
	}
	if !add {
		script.WriteString("exit $status\n")
	}
	return []string{"/bin/sh", "-c", script.String()}
}

func runPolicyBatch(batch policyRouteBatch, add bool, cmds netCmds, run func([]string) error) error {
	if batch.Tunnel == "" || len(batch.Tunnel) > 256 {
		return fmt.Errorf("invalid routing tunnel")
	}
	for _, prefix := range batch.Prefixes {
		parsed, err := netip.ParsePrefix(prefix)
		if err != nil || !parsed.Addr().Is4() || parsed.Bits() == 0 {
			return fmt.Errorf("invalid IPv4 route prefix")
		}
	}
	// Keep command lines bounded on Windows and Unix. One process handles a
	// chunk so large GeoIP selections do not start a shell for each prefix.
	var first error
	for start := 0; start < len(batch.Prefixes); start += 64 {
		part := policyRouteBatch{Tunnel: batch.Tunnel, Prefixes: batch.Prefixes[start:min(start+64, len(batch.Prefixes))]}
		if err := run(policyBatchCommand(part, add, cmds)); err != nil {
			if add {
				return err
			}
			if first == nil {
				first = err
			}
		}
	}
	return first
}
