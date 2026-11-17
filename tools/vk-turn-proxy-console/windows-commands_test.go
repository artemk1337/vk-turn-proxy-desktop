package main

import (
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf16"
)

func decodePowerShell(argv []string) string {
	b, _ := base64.StdEncoding.DecodeString(argv[len(argv)-1])
	units := make([]uint16, len(b)/2)
	for i := range units {
		units[i] = binary.LittleEndian.Uint16(b[i*2:])
	}
	return string(utf16.Decode(units))
}

func TestWindowsScriptsQuoteInterfaceNames(t *testing.T) {
	name := "Wi-Fi 'office'; Write-Host broken"
	add, del := (windowsCmds{}).pin("203.0.113.1", gateway{IP: "192.168.1.1", Iface: name})
	script := decodePowerShell(add)
	if !strings.Contains(script, "'Wi-Fi ''office''; Write-Host broken'") {
		t.Fatal("interface must be one PowerShell string literal")
	}
	if !strings.Contains(decodePowerShell(del), "$_.RouteMetric -eq 42757") {
		t.Fatal("rollback must remove only client-owned routes")
	}
	if !strings.Contains(script, "[Console]::OutputEncoding=[System.Text.UTF8Encoding]::new($false)") {
		t.Fatal("JSON with localized interface names must be returned as UTF-8")
	}
}

func TestWindowsSplitRoutesPreserveDefaultRoute(t *testing.T) {
	for _, route := range (windowsCmds{}).splitRoutes("VKTurnProxy") {
		if route.key != "0.0.0.0/1" && route.key != "128.0.0.0/1" {
			t.Fatal(route.key)
		}
		for _, argv := range [][]string{route.add, route.del} {
			if strings.Contains(decodePowerShell(argv), "'0.0.0.0/0'") {
				t.Fatal("must preserve default route")
			}
		}
	}
}

func TestWindowsRepinRecreatesRouteOnNewInterface(t *testing.T) {
	script := decodePowerShell((windowsCmds{}).repin("203.0.113.1", gateway{IP: "10.0.0.1", Iface: "Ethernet"}))
	if !strings.Contains(script, "Remove-NetRoute") || !strings.Contains(script, "-InterfaceAlias 'Ethernet'") || !strings.Contains(script, "-NextHop '10.0.0.1'") {
		t.Fatal(script)
	}
	if strings.Index(script, "Remove-NetRoute") > strings.Index(script, "New-NetRoute") {
		t.Fatal("remove old interface route before adding new")
	}
}

func TestWindowsDefaultRouteSuppressesModuleProgress(t *testing.T) {
	script := decodePowerShell(windowsDefaultRouteCommand())
	progress := strings.Index(script, "$ProgressPreference='SilentlyContinue';")
	query := strings.Index(script, "Get-NetRoute ")
	if progress < 0 || query < 0 || progress > query {
		t.Fatal("disable module progress before querying routes", script)
	}
	if !strings.Contains(script, "$ErrorActionPreference='Stop';") {
		t.Fatal("route query errors must still fail the command", script)
	}
}

func TestWindowsDefaultRouteAndDNSParsing(t *testing.T) {
	gw, ok := parseWindowsDefaultRoute(`{"NextHop":"192.168.1.1","InterfaceAlias":"Wi-Fi"}`)
	if !ok || gw.IP != "192.168.1.1" || gw.Iface != "Wi-Fi" {
		t.Fatal(gw, ok)
	}
	gw, ok = parseWindowsDefaultRoute(`{"NextHop":"192.168.1.1","InterfaceAlias":"Беспроводная сеть"}`)
	if !ok || gw.Iface != "Беспроводная сеть" {
		t.Fatal("localized interface names must survive parsing", gw)
	}
	for _, text := range []string{"", "null", `{"NextHop":"bad","InterfaceAlias":"Wi-Fi"}`} {
		if _, ok := parseWindowsDefaultRoute(text); ok {
			t.Fatal(text)
		}
	}
	servers := parseWindowsDNS(`["192.168.1.1","bad","192.168.1.1"]`)
	if len(servers) != 1 || servers[0] != "192.168.1.1" {
		t.Fatal(servers)
	}
}

func TestWindowsDNSRecordsUndoBeforeChange(t *testing.T) {
	j := newJournal(filepath.Join(t.TempDir(), "state.json"))
	called := false
	d := &windowsDNS{j: j, run: func(argv []string) (string, error) {
		called = true
		state, err := readState(j.path)
		if err != nil || state == nil || len(state.Undo) != 1 {
			t.Fatal("DNS change without persisted undo", err)
		}
		if !strings.Contains(decodePowerShell(state.Undo[0].Argv), "-ResetServerAddresses") {
			t.Fatal("missing DNS undo")
		}
		return "", errors.New("set DNS failed")
	}}
	if err := d.apply("VKTurnProxy", []string{"1.1.1.1"}); err == nil || !called {
		t.Fatal("must surface DNS failure")
	}
	if !j.has("dns windows VKTurnProxy") {
		t.Fatal("failed change must keep recovery entry")
	}
}

func TestWindowsDNSWriteFailureDoesNotChangeSystem(t *testing.T) {
	d := &windowsDNS{j: newJournal(filepath.Join(t.TempDir(), "missing", "state.json")), run: func([]string) (string, error) { t.Fatal("must not execute without journal"); return "", nil }}
	if err := d.apply("VKTurnProxy", []string{"1.1.1.1"}); err == nil {
		t.Fatal("expected journal failure")
	}
}

func TestWindowsRouteCleanupVerifiesRemoval(t *testing.T) {
	_, del := windowsRoute("8000::/1", "VKTurnProxy", "::")
	script := decodePowerShell(del)
	for _, want := range []string{
		"Get-NetRoute -PolicyStore ActiveStore -ErrorAction Stop",
		"$_.DestinationPrefix -eq '8000::/1' -and $_.RouteMetric -eq 42757",
		"$attempt -lt 3 -and $remaining.Count -gt 0",
		"Remove-NetRoute -Confirm:$false -ErrorAction Stop",
		"catch { $removeError = $_ }",
		"throw $removeError",
		"; exit 0",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("missing %q in %s", want, script)
		}
	}
	if strings.Contains(script, "SilentlyContinue") {
		t.Fatal("query failures must be reported")
	}
}

func TestWindowsLegacyRouteRecovery(t *testing.T) {
	for _, tc := range []struct{ key, prefix string }{
		{"route split 0.0.0.0/1", "0.0.0.0/1"},
		{"route split 128.0.0.0/1", "128.0.0.0/1"},
		{"route ipv6 ::/1", "::/1"},
		{"route ipv6 8000::/1", "8000::/1"},
		{"route via 203.0.113.1", "203.0.113.1/32"},
		{"pin 203.0.113.2", "203.0.113.2/32"},
	} {
		t.Run(tc.key, func(t *testing.T) {
			legacy := powershell(fmt.Sprintf("Get-NetRoute -DestinationPrefix %s -PolicyStore ActiveStore -ErrorAction SilentlyContinue | Where-Object { $_.RouteMetric -eq %d } | Remove-NetRoute -Confirm:$false", psQuote(tc.prefix), windowsRouteMetric))
			path := filepath.Join(t.TempDir(), "state.json")
			j := newJournal(path)
			if err := j.add(undoStep{Key: tc.key, Argv: legacy}); err != nil {
				t.Fatal(err)
			}
			_, want := windowsRoute(tc.prefix, "", "")
			called := false
			if err := recoverLeftovers(path, func(argv []string) error {
				called = true
				if !slices.Equal(argv, want) {
					t.Fatal("legacy cleanup was not upgraded")
				}
				return nil
			}, t.Logf); err != nil {
				t.Fatal(err)
			}
			if !called {
				t.Fatal("cleanup did not execute")
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatal("journal remains", err)
			}
		})
	}
}

func TestWindowsUndoPreservesOtherCommands(t *testing.T) {
	for _, step := range []undoStep{
		{Key: "route split 0.0.0.0/1", Argv: []string{"route", "delete", "0.0.0.0/1"}},
		{Key: "dns windows VKTurnProxy", Argv: powershell("Reset-DnsClientServerAddress")},
		{Key: "pin invalid", Argv: powershell("Write-Output test")},
	} {
		if !slices.Equal(windowsRouteUndo(step), step.Argv) {
			t.Fatal("unrelated undo changed", step.Key)
		}
	}
}

func TestWindowsCleanupFailureKeepsLegacyJournal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	j := newJournal(path)
	legacy := powershell("Get-NetRoute -DestinationPrefix '::/1' -PolicyStore ActiveStore -ErrorAction SilentlyContinue | Where-Object { $_.RouteMetric -eq 42757 } | Remove-NetRoute -Confirm:$false")
	if err := j.add(undoStep{Key: "route ipv6 ::/1", Argv: legacy}); err != nil {
		t.Fatal(err)
	}
	if err := recoverLeftovers(path, func([]string) error { return os.ErrPermission }, t.Logf); err == nil {
		t.Fatal("cleanup failure lost")
	}
	doc, err := readState(path)
	if err != nil || doc == nil || len(doc.Undo) != 1 {
		t.Fatal("failed cleanup must remain recoverable", doc, err)
	}
}
