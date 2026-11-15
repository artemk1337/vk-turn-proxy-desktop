// SPDX-License-Identifier: MIT
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	routingpolicy "github.com/artemk1337/vk-turn-proxy-desktop/pkg/routing-policy"
)

func TestRoutingSettingsPersistPerProfileAndRemainDetached(t *testing.T) {
	a := testApp(t)
	a.profiles = profileStore{Profiles: []Profile{{ID: "one", Name: "One", Mode: "link"}, {ID: "two", Name: "Two", Mode: "link"}}, ActiveID: "two"}
	settings := routingpolicy.Settings{Mode: "exclude", GeoIP: []string{"ru"}, GeoSite: []string{"youtube"}, Custom: "example.org", LocalDirect: true}
	state, err := a.SaveRouting("one", settings)
	if err != nil {
		t.Fatal(err)
	}
	settings.GeoIP[0] = "changed"
	state.Profiles[0].Routing.GeoSite[0] = "changed"
	if a.profiles.ActiveID != "two" || a.profiles.Profiles[0].Routing.GeoIP[0] != "ru" || a.profiles.Profiles[0].Routing.GeoSite[0] != "youtube" {
		t.Fatal("routing save changed selection or aliased caller slices")
	}
	store, err := loadProfiles(a.root)
	if err != nil || store.Profiles[0].Routing.Mode != "exclude" || store.Profiles[1].Routing.Mode != "" {
		t.Fatalf("routing was not persisted per profile: %+v %v", store, err)
	}
	if _, err := a.SaveRouting("one", routingpolicy.Settings{Mode: "invalid"}); err == nil {
		t.Fatal("invalid routing accepted")
	}
	if a.profiles.Profiles[0].Routing.Mode != "exclude" {
		t.Fatal("invalid settings replaced persisted policy")
	}
}

func TestRoutingArgumentsUseBoundedSnapshot(t *testing.T) {
	a := testApp(t)
	args, err := a.routingArguments(routingpolicy.Settings{}, a.root)
	if err != nil || len(args) != 0 {
		t.Fatalf("legacy profile gained routing arguments: %v %v", args, err)
	}
	settings := routingpolicy.Settings{Mode: "include", Custom: "192.0.2.0/24\nexample.org", LocalDirect: true}
	args, err = a.routingArguments(settings, a.root)
	if err != nil || len(args) != 2 || args[0] != "-routing-file" {
		t.Fatalf("routing arguments failed: %v %v", args, err)
	}
	raw, err := os.ReadFile(args[1])
	if err != nil {
		t.Fatal(err)
	}
	var policy routingpolicy.Policy
	if err := json.Unmarshal(raw, &policy); err != nil || policy.Validate() != nil || len(policy.Domains) != 1 || len(policy.Prefixes) != 1 {
		t.Fatalf("invalid routing snapshot: %s, %v", raw, err)
	}
	settings.GeoIP = []string{"ru"}
	if _, err := a.routingArguments(settings, a.root); err == nil {
		t.Fatal("missing GeoIP data silently ignored")
	}
}

func TestRoutingCatalogAndAutoUpdateDefaults(t *testing.T) {
	a := testApp(t)
	catalog, err := a.GetRoutingCatalog()
	if err != nil || catalog.GeoIP == nil || catalog.GeoSite == nil {
		t.Fatalf("empty catalog not available: %+v %v", catalog, err)
	}
	if _, err := a.SetRoutingAutoUpdate(true); err != nil {
		t.Fatal(err)
	}
	prefs, err := loadPreferences(a.root)
	if err != nil || !prefs.RoutingAutoUpdate {
		t.Fatalf("auto update not persisted: %+v %v", prefs, err)
	}
	if err := writePrivate(filepath.Join(a.root, "preferences.json"), []byte(`{"theme":"dark"}`)); err != nil {
		t.Fatal(err)
	}
	prefs, err = loadPreferences(a.root)
	if err != nil || !prefs.RoutingAutoUpdate {
		t.Fatal("legacy preferences lost routing auto-update default")
	}
	var profile Profile
	if err := json.Unmarshal([]byte(`{"id":"legacy"}`), &profile); err != nil || profile.Routing.Mode != "" || !profile.Routing.LocalDirect {
		t.Fatal("legacy routing defaults were not restored")
	}
}

func TestRoutingReadyAdvancesConnectionState(t *testing.T) {
	a := testApp(t)
	a.connection = "handshake"
	a.appendLog("2026/11/15 22:00:00 routing: ready, exclude mode")
	if a.GetState().Connection.State != "connected" {
		t.Fatal("routing activation did not complete the GUI connection phase")
	}
}
