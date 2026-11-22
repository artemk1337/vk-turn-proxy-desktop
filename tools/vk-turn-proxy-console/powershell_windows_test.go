// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"testing"
)

func TestPowerShellProgressDoesNotCorruptPolicyDNS(t *testing.T) {
	// Exercise the real encoded PowerShell command and combined output, as used
	// by applyPolicy. Progress must not add a #< CLIXML header to the JSON.
	out, err := runCmd(powershell(`Write-Progress -Activity 'Loading network module' -Status 'Loading' -PercentComplete 50; '[{"guid":"{test}","alias":"Ethernet","servers":["192.0.2.1"],"static":false}]'`))
	if err != nil {
		t.Fatal(err)
	}
	var states []windowsPolicyDNS
	if err := json.Unmarshal([]byte(out), &states); err != nil {
		t.Fatalf("policy DNS output is not JSON: %v; output=%q", err, out)
	}
	if len(states) != 1 || states[0].GUID != "{test}" || len(states[0].Servers) != 1 || states[0].Servers[0] != "192.0.2.1" {
		t.Fatalf("unexpected DNS snapshot: %+v", states)
	}
}
