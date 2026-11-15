// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"errors"
	"strings"
)

type windowsPolicyDNS struct {
	GUID    string   `json:"guid"`
	Alias   string   `json:"alias"`
	Servers []string `json:"servers"`
	Static  bool     `json:"static"`
}

// Split routing still needs the physical interfaces to use the local DNS
// forwarder. Capture each IPv4 configuration before changing it, preserving
// the distinction between static addresses and DHCP-managed DNS.
func (d *windowsDNS) applyPolicy() error {
	const snapshot = `$states = @(Get-NetIPInterface -AddressFamily IPv4 -ConnectionState Connected | ForEach-Object {
 $adapter = Get-NetAdapter -InterfaceIndex $_.InterfaceIndex -IncludeHidden -ErrorAction SilentlyContinue
 if ($null -ne $adapter) {
  $guid = '{' + $adapter.InterfaceGuid.ToString().Trim('{}') + '}'
  $registry = Get-ItemProperty -LiteralPath ('HKLM:\SYSTEM\CurrentControlSet\Services\Tcpip\Parameters\Interfaces\' + $guid) -ErrorAction Stop
  $dns = Get-DnsClientServerAddress -InterfaceIndex $_.InterfaceIndex -AddressFamily IPv4 -ErrorAction Stop
  [PSCustomObject]@{ guid=$guid; alias=$_.InterfaceAlias; servers=@($dns.ServerAddresses); static=([bool]$registry.NameServer -and $registry.NameServer -ne '127.0.0.1') }
 }
}); ConvertTo-Json -Compress -Depth 4 -InputObject $states`
	out, err := d.run(powershell(snapshot))
	if err != nil {
		return err
	}
	var states []windowsPolicyDNS
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &states); err != nil {
		return err
	}
	if len(states) == 0 {
		return errors.New("no network interface is available for policy DNS")
	}
	if d.policy == nil {
		d.policy = make(map[string]windowsPolicyDNS)
	}
	for _, state := range states {
		key := "dns policy windows " + state.GUID
		if original, exists := d.policy[state.GUID]; exists {
			original.Alias = state.Alias
			d.policy[state.GUID] = original
			if len(state.Servers) == 1 && state.Servers[0] == "127.0.0.1" {
				continue
			}
			// A user or DHCP changed DNS while connected. Preserve that newer
			// configuration before restoring our local forwarder.
			if err := d.j.done(key); err != nil {
				return err
			}
		}
		lookup := "$adapter = Get-NetAdapter -IncludeHidden | Where-Object { $_.InterfaceGuid.ToString().Trim('{}') -eq " + psQuote(strings.Trim(state.GUID, "{}")) + " }; if ($null -ne $adapter) { $dns = Get-DnsClientServerAddress -InterfaceIndex $adapter.InterfaceIndex -AddressFamily IPv4 -ErrorAction Stop; "
		restore := "Set-DnsClientServerAddress -InputObject $dns -ResetServerAddresses"
		if state.Static && len(state.Servers) > 0 {
			servers := make([]string, len(state.Servers))
			for i, server := range state.Servers {
				servers[i] = psQuote(server)
			}
			restore = "Set-DnsClientServerAddress -InputObject $dns -ServerAddresses @(" + strings.Join(servers, ",") + ")"
		}
		undo := powershell(lookup + restore + "; Clear-DnsClientCache }")
		if err := d.j.add(undoStep{Key: key, Argv: undo}); err != nil {
			return err
		}
		if _, err := d.run(powershell(lookup + "Set-DnsClientServerAddress -InputObject $dns -ServerAddresses @('127.0.0.1'); Clear-DnsClientCache }")); err != nil {
			return err
		}
		d.policy[state.GUID] = state
	}
	return nil
}
