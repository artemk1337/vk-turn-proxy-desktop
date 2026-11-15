// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"strings"
)

type windowsDNS struct {
	j      *journal
	run    func([]string) (string, error)
	orig   []string
	policy map[string]windowsPolicyDNS
}

func (d *windowsDNS) originals() []string { return d.orig }

func (d *windowsDNS) apply(tun string, servers []string) error {
	if len(servers) == 1 && servers[0] == "127.0.0.1" {
		return d.applyPolicy()
	}
	if len(servers) == 0 {
		return fmt.Errorf("the tunnel DNS list is empty")
	}
	var quoted []string
	for _, server := range servers {
		if net.ParseIP(server) == nil {
			return fmt.Errorf("invalid tunnel DNS address")
		}
		quoted = append(quoted, psQuote(server))
	}
	alias := psQuote(tun)
	undo := powershell("if (Get-NetAdapter -Name " + alias + " -ErrorAction SilentlyContinue) { Set-DnsClientServerAddress -InterfaceAlias " + alias + " -ResetServerAddresses; Clear-DnsClientCache }")
	if err := d.j.add(undoStep{Key: "dns windows " + tun, Argv: undo}); err != nil {
		return err
	}
	_, err := d.run(powershell("Set-DnsClientServerAddress -InterfaceAlias " + alias + " -ServerAddresses @(" + strings.Join(quoted, ",") + "); Clear-DnsClientCache"))
	return err
}

func (d *windowsDNS) refresh(gw gateway) []string {
	if gw.Iface == "" {
		return d.orig
	}
	if d.policy != nil {
		if err := d.applyPolicy(); err != nil {
			log.Printf("dns: configuring a new interface for policy DNS: %v", err)
		}
		for _, state := range d.policy {
			if state.Alias == gw.Iface {
				if state.Static {
					d.orig = append([]string{}, state.Servers...)
				} else {
					path := "HKLM:\\SYSTEM\\CurrentControlSet\\Services\\Tcpip\\Parameters\\Interfaces\\" + state.GUID
					out, err := d.run(powershell("$value = (Get-ItemProperty -LiteralPath " + psQuote(path) + " -ErrorAction Stop).DhcpNameServer; ConvertTo-Json -Compress -InputObject @($value -split '[,;\\s]+' | Where-Object { $_ })"))
					if err == nil {
						d.orig = parseWindowsDNS(out)
					}
				}
				return d.orig
			}
		}
		return d.orig
	}
	script := "ConvertTo-Json -Compress -InputObject @((Get-DnsClientServerAddress -InterfaceAlias " + psQuote(gw.Iface) + " -AddressFamily IPv4).ServerAddresses)"
	if out, err := d.run(powershell(script)); err == nil {
		if servers := parseWindowsDNS(out); len(servers) > 0 {
			d.orig = servers
		}
	}
	return d.orig
}

func parseWindowsDNS(out string) []string {
	var values []string
	if json.Unmarshal([]byte(strings.TrimSpace(out)), &values) != nil {
		return nil
	}
	var outIPs []string
	for _, value := range values {
		if ip := net.ParseIP(value); ip != nil {
			outIPs = append(outIPs, ip.String())
		}
	}
	return dedupe(outIPs)
}
