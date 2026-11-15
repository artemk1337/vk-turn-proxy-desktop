// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	routingdata "github.com/artemk1337/vk-turn-proxy-desktop/pkg/routing-data"
)

func testApp(t *testing.T) *App {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	root := t.TempDir()
	if err := secureDirectory(root); err != nil {
		t.Fatal(err)
	}
	return &App{root: root, routingData: routingdata.NewStore(filepath.Join(root, "routing-data")), profiles: profileStore{Profiles: []Profile{}}, preferences: normalizePreferences(Preferences{}),
		ctx: ctx, cancel: cancel, quit: func() {}, connection: "disconnected", logs: Logs{Lines: []string{}}, frontendReady: make(chan struct{})}
}

func TestPreferencesNormalizeAndPersist(t *testing.T) {
	a := testApp(t)
	state, err := a.SavePreferences(Preferences{Language: "ru", Theme: "dark", LogLines: 1})
	if err != nil {
		t.Fatal(err)
	}
	if state.Preferences.Language != "ru" || state.Preferences.Theme != "dark" || state.Preferences.LogLines != defaults.LogLines.Min {
		t.Fatalf("unexpected preferences: %+v", state.Preferences)
	}
	loaded, err := loadPreferences(a.root)
	if err != nil || loaded != state.Preferences {
		t.Fatalf("preferences were not restored: %+v, %v", loaded, err)
	}
	state, err = a.SavePreferences(Preferences{Language: "unsupported", Theme: "invalid", LogLines: defaults.LogLines.Max + 1})
	if err != nil {
		t.Fatal(err)
	}
	if state.Preferences.Theme != defaults.DefaultTheme || state.Preferences.LogLines != defaults.LogLines.Max {
		t.Fatalf("invalid normalization: %+v", state.Preferences)
	}
}

func TestLegacyStoreCaseCompatibility(t *testing.T) {
	a := testApp(t)
	name, _ := storeNames()
	legacy := []byte(`{"Profiles":[{"ID":"old","Name":"Existing","Mode":"link","ProfileText":"secret","UseSRTP":true}],"ActiveID":"old"}`)
	if err := writeSecret(filepath.Join(a.root, name), legacy); err != nil {
		t.Fatal(err)
	}
	store, err := loadProfiles(a.root)
	if err != nil {
		t.Fatal(err)
	}
	if store.ActiveID != "old" || len(store.Profiles) != 1 || store.Profiles[0].ProfileText != "secret" {
		t.Fatalf("legacy profile lost: %+v", store)
	}
}

func TestLegacyProfileTransportDefault(t *testing.T) {
	var p Profile
	if err := json.Unmarshal([]byte(`{"id":"legacy","name":"Legacy","mode":"wireguard"}`), &p); err != nil {
		t.Fatal(err)
	}
	if !p.UseSRTP {
		t.Fatal("legacy profile lost the default SRTP transport")
	}
	if err := json.Unmarshal([]byte(`{"id":"dtls","name":"DTLS","mode":"wireguard","useSRTP":false}`), &p); err != nil {
		t.Fatal(err)
	}
	if p.UseSRTP {
		t.Fatal("explicit DTLS transport was overwritten")
	}
}

func TestProfileSelectionAndDeletionPersistBeforeState(t *testing.T) {
	a := testApp(t)
	a.profiles = profileStore{Profiles: []Profile{{ID: "one", Name: "One", Mode: "link"}, {ID: "two", Name: "Two", Mode: "link"}}, ActiveID: "one"}
	state, err := a.SelectProfile("two")
	if err != nil || state.ActiveID != "two" {
		t.Fatalf("selection failed: %v", err)
	}
	restored, err := newApp(a.root, "", func() {})
	if err != nil {
		t.Fatal(err)
	}
	defer restored.cancel()
	if restored.GetState().ActiveID != "two" {
		t.Fatal("last selected profile was not restored after restart")
	}
	state, err = a.DeleteProfile("two")
	if err != nil || state.ActiveID != "one" || len(state.Profiles) != 1 {
		t.Fatalf("deletion failed: %+v %v", state, err)
	}
	store, err := loadProfiles(a.root)
	if err != nil || store.ActiveID != "one" {
		t.Fatalf("selection was not persisted: %v", err)
	}
	a.session = &backendSession{}
	if _, err := a.DeleteProfile("one"); err == nil {
		t.Fatal("changed profiles during an active session")
	}
}

func TestFailedPersistenceRetainsSelection(t *testing.T) {
	a := testApp(t)
	a.profiles = profileStore{Profiles: []Profile{{ID: "one", Name: "One", Mode: "link"}, {ID: "two", Name: "Two", Mode: "link"}}, ActiveID: "one"}
	a.root = filepath.Join(a.root, "missing", "child")
	if err := os.WriteFile(filepath.Dir(a.root), []byte("file"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SelectProfile("two"); err == nil {
		t.Fatal("expected storage failure")
	}
	if a.profiles.ActiveID != "one" {
		t.Fatal("selection changed despite storage failure")
	}
}

func TestProfileArgumentsKeepSecretsInFiles(t *testing.T) {
	a := testApp(t)
	p := Profile{Mode: "wireguard", WGConfig: "private-key-data", VKLink: "https://vk.ru/call/join/private", UseSRTP: true, Endpoint: "relay.example:56000"}
	args, err := profileArguments(p, a.root)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	if strings.Contains(joined, p.WGConfig) || strings.Contains(joined, p.VKLink) {
		t.Fatal("secret in command arguments")
	}
	raw, err := os.ReadFile(filepath.Join(a.root, "profile.txt"))
	if err != nil || string(raw) != p.WGConfig {
		t.Fatal("profile data was not preserved")
	}
}

func TestProgressAndLogsRemainBounded(t *testing.T) {
	a := testApp(t)
	a.connection = "credentials"
	a.appendLog("TURN relay allocated: relay")
	if a.GetState().Connection.State != "relay" {
		t.Fatal("relay phase lost")
	}
	a.appendLog("proxy: first connection up")
	a.appendLog("DEFAULT ROUTE → the tunnel")
	a.appendLog("TURN relay allocated: stale")
	if a.GetState().Connection.State != "connected" {
		t.Fatal("progress moved backwards")
	}
	for range 300 {
		a.appendLog(strings.Repeat("x", 20000))
	}
	logs := a.GetLogs()
	if len(logs.Lines) > a.preferences.LogLines || a.logBytes > defaults.LogMaxBytes {
		t.Fatal("log limits exceeded")
	}
	logs.Lines[0] = "mutated"
	if a.GetLogs().Lines[0] == "mutated" {
		t.Fatal("log snapshot aliases mutable state")
	}
	if _, err := a.ClearLogs(); err != nil || len(a.GetLogs().Lines) != 0 {
		t.Fatal("logs were not cleared")
	}
}

func TestLogReaderDiscardsOversizedLineAndRetainsPhase(t *testing.T) {
	input := strings.Repeat("x", 40000) + "\nTURN relay allocated: local\n"
	var lines []string
	readBackendLogs(strings.NewReader(input), func(line string) { lines = append(lines, line) })
	if len(lines) != 2 || len(lines[0]) > 8220 || logPhase(lines[1]) != "relay" {
		t.Fatalf("unexpected bounded logs: %d", len(lines))
	}
}

func TestStatePreferencesNeverContainCookies(t *testing.T) {
	a := testApp(t)
	a.cookies = "secret-cookie-value"
	_, err := a.SavePreferences(a.preferences)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(a.root, "preferences.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), a.cookies) {
		t.Fatal("cookies leaked to preferences")
	}
	data, err = json.Marshal(a.GetState())
	if err != nil || strings.Contains(string(data), a.cookies) {
		t.Fatal("cookies leaked to state")
	}
}

func TestCorruptStorageRecoversWithVisibleWarning(t *testing.T) {
	root := t.TempDir()
	if err := secureDirectory(root); err != nil {
		t.Fatal(err)
	}
	if err := writePrivate(filepath.Join(root, "preferences.json"), []byte(`{"theme":"broken"`)); err != nil {
		t.Fatal(err)
	}
	profilesName, authName := storeNames()
	if err := writeSecret(filepath.Join(root, profilesName), []byte("invalid-json")); err != nil {
		t.Fatal(err)
	}
	if err := writePrivate(filepath.Join(root, authName), []byte(strings.Repeat("x", maxInput+(1<<20)+1))); err != nil {
		t.Fatal(err)
	}
	a, err := newApp(root, "missing-backend", func() {})
	if err != nil {
		t.Fatalf("recoverable store error prevented startup: %v", err)
	}
	t.Cleanup(a.cancel)
	state := a.GetState()
	if state.Error == "" || state.VK.Status != "signed_out" || state.Profiles == nil || len(state.Profiles) != 0 || state.Preferences.Theme != defaults.DefaultTheme {
		t.Fatalf("corrupt store was not recovered: %+v", state)
	}
	data, err := os.ReadFile(filepath.Join(root, profilesName))
	if err != nil || len(data) == 0 {
		t.Fatal("corrupt original was unexpectedly removed")
	}
}

func TestInvalidCookieImportPreservesStoredAccount(t *testing.T) {
	a := testApp(t)
	a.cookies = "remixsid=old-session; p=old-token"
	_, name := storeNames()
	path := filepath.Join(a.root, name)
	if err := writeSecret(path, []byte(a.cookies)); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"invalid", "remixsid=missing-token", ".vk.ru\tTRUE\t/\tTRUE\t1\tremixsid\texpired\n.login.vk.ru\tTRUE\t/\tTRUE\t1\tp\texpired"} {
		if _, err := a.ImportCookies(text); err == nil {
			t.Fatal("invalid cookies were accepted")
		}
		stored, err := readSecret(path, maxInput)
		if err != nil || string(stored) != a.cookies {
			t.Fatal("invalid import replaced the existing account")
		}
	}
}

func TestDisconnectWritesStopFileAndRetainsState(t *testing.T) {
	a := testApp(t)
	stop := filepath.Join(a.root, "stop")
	a.session = &backendSession{stopFile: stop}
	a.connection = "connected"
	state, err := a.Disconnect()
	if err != nil || state.Connection.State != "disconnecting" {
		t.Fatalf("disconnect failed: %v", err)
	}
	if _, err := os.Stat(stop); err != nil {
		t.Fatal("normal cleanup was not requested")
	}
	a.appendLog("DEFAULT ROUTE → the tunnel")
	if a.GetState().Connection.State != "disconnecting" {
		t.Fatal("log restored connected state during cleanup")
	}
}

func TestProfileTURNTransport(t *testing.T) {
	for _, mode := range []string{"link", "wireguard"} {
		for _, transport := range []string{"", "tcp", "udp", "invalid"} {
			t.Run(mode+"/"+transport, func(t *testing.T) {
				p := Profile{ID: "one", Name: "Test", Mode: mode, ProfileText: "profile", WGConfig: "config", VKLink: "https://vk.ru/call/join/test", TURNTransport: transport}
				root := t.TempDir()
				if err := saveProfiles(root, profileStore{Profiles: []Profile{p}}); err != nil {
					t.Fatal(err)
				}
				stored, err := loadProfiles(root)
				if err != nil {
					t.Fatal(err)
				}
				p = stored.Profiles[0]
				if p.TURNTransport != transport {
					t.Fatal("transport was not preserved")
				}
				args, err := profileArguments(p, root)
				if transport == "invalid" {
					if err == nil {
						t.Fatal("invalid transport accepted")
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				joined := strings.Join(args, " ")
				if transport == "" {
					if strings.Contains(joined, "-turn-transport") {
						t.Fatal("profile transport overridden")
					}
				} else if !strings.Contains(joined, "-turn-transport "+transport) {
					t.Fatal("missing transport override")
				}
			})
		}
	}
}

func TestProfileConnectionCount(t *testing.T) {
	for _, mode := range []string{"link", "wireguard"} {
		for _, count := range []int{0, 1, 30, 120, -1, 121} {
			p := Profile{Mode: mode, ProfileText: "profile", WGConfig: "config", VKLink: "https://vk.ru/call/join/test", NumConnections: count}
			args, err := profileArguments(p, t.TempDir())
			if count < 0 || count > 120 {
				if err == nil {
					t.Fatalf("accepted %d", count)
				}
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			expected := count
			if expected == 0 {
				expected = 30
			}
			if !strings.Contains(strings.Join(args, " "), "-conns "+strconv.Itoa(expected)) {
				t.Fatalf("missing count in %v", args)
			}
		}
	}
	var legacy Profile
	if err := json.Unmarshal([]byte(`{"name":"legacy"}`), &legacy); err != nil {
		t.Fatal(err)
	}
	if legacy.NumConnections != 30 {
		t.Fatal("missing legacy default")
	}
}

func TestProfileMTU(t *testing.T) {
	for _, mode := range []string{"link", "wireguard"} {
		for _, mtu := range []int{0, 1000, 1280, 1400, 999, 1401, -1} {
			p := Profile{Mode: mode, ProfileText: "profile", WGConfig: "config", VKLink: "https://vk.ru/call/join/test", MTU: mtu}
			args, err := profileArguments(p, t.TempDir())
			if mtu != 0 && (mtu < 1000 || mtu > 1400) {
				if err == nil {
					t.Fatalf("accepted MTU %d", mtu)
				}
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			expected := mtu
			if expected == 0 {
				expected = 1280
			}
			if !strings.Contains(strings.Join(args, " "), "-mtu "+strconv.Itoa(expected)) {
				t.Fatalf("missing MTU in %v", args)
			}
		}
	}
	var legacy Profile
	if err := json.Unmarshal([]byte(`{"name":"legacy"}`), &legacy); err != nil {
		t.Fatal(err)
	}
	if legacy.MTU != 1280 {
		t.Fatal("missing legacy MTU default")
	}
	root := t.TempDir()
	p := Profile{ID: "test", Name: "Test", Mode: "link", MTU: 1350}
	if err := saveProfiles(root, profileStore{Profiles: []Profile{p}}); err != nil {
		t.Fatal(err)
	}
	stored, err := loadProfiles(root)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Profiles[0].MTU != 1350 {
		t.Fatal("MTU was not preserved")
	}
}
