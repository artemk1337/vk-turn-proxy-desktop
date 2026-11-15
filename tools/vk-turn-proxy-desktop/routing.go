// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"time"

	routingdata "github.com/artemk1337/vk-turn-proxy-desktop/pkg/routing-data"
	routingpolicy "github.com/artemk1337/vk-turn-proxy-desktop/pkg/routing-policy"
)

type RoutingCatalog struct {
	GeoIP      []routingdata.Entry `json:"geoip"`
	GeoSite    []routingdata.Entry `json:"geosite"`
	UpdatedAt  string              `json:"updatedAt"`
	AutoUpdate bool                `json:"autoUpdate"`
}

func routingCatalog(catalog routingdata.Catalog, autoUpdate bool) RoutingCatalog {
	updatedAt := ""
	if !catalog.UpdatedAt.IsZero() {
		updatedAt = catalog.UpdatedAt.Format(time.RFC3339)
	}
	return RoutingCatalog{GeoIP: catalog.GeoIP, GeoSite: catalog.GeoSite, UpdatedAt: updatedAt, AutoUpdate: autoUpdate}
}

func (a *App) GetRoutingCatalog() (RoutingCatalog, error) {
	catalog, err := a.routingData.Catalog()
	if errors.Is(err, os.ErrNotExist) {
		catalog = routingdata.Catalog{GeoIP: []routingdata.Entry{}, GeoSite: []routingdata.Entry{}}
		err = nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return routingCatalog(catalog, a.preferences.RoutingAutoUpdate), err
}

func (a *App) UpdateRoutingData() (RoutingCatalog, error) {
	a.routingMu.Lock()
	defer a.routingMu.Unlock()
	catalog, err := a.routingData.Update(a.ctx)
	a.mu.Lock()
	defer a.mu.Unlock()
	return routingCatalog(catalog, a.preferences.RoutingAutoUpdate), err
}

func (a *App) SetRoutingAutoUpdate(enabled bool) (RoutingCatalog, error) {
	a.mu.Lock()
	prefs := a.preferences
	prefs.RoutingAutoUpdate = enabled
	raw, err := json.Marshal(prefs)
	if err == nil {
		err = writePrivate(filepath.Join(a.root, "preferences.json"), raw)
	}
	if err == nil {
		a.preferences = prefs
	}
	a.mu.Unlock()
	if err != nil {
		return RoutingCatalog{}, err
	}
	if enabled {
		go a.updateRoutingIfNeeded()
	}
	return a.GetRoutingCatalog()
}

func cloneRouting(settings routingpolicy.Settings) routingpolicy.Settings {
	settings.GeoIP = slices.Clone(settings.GeoIP)
	settings.GeoSite = slices.Clone(settings.GeoSite)
	return settings
}

func (a *App) SaveRouting(id string, settings routingpolicy.Settings) (State, error) {
	if err := settings.Validate(); err != nil {
		return a.GetState(), err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	index := slices.IndexFunc(a.profiles.Profiles, func(p Profile) bool { return p.ID == id })
	if index < 0 {
		return a.snapshot(), errors.New("Selected profile does not exist.")
	}
	store := profileStore{Profiles: slices.Clone(a.profiles.Profiles), ActiveID: a.profiles.ActiveID}
	store.Profiles[index].Routing = cloneRouting(settings)
	if err := saveProfiles(a.root, store); err != nil {
		return a.snapshot(), err
	}
	a.profiles = store
	return a.snapshot(), nil
}

func (a *App) routingArguments(settings routingpolicy.Settings, directory string) ([]string, error) {
	if err := settings.Validate(); err != nil {
		return nil, err
	}
	if settings.Mode == "" {
		return nil, nil
	}
	prefixes, domains, err := routingpolicy.ParseCustom(settings.Custom)
	if err != nil {
		return nil, err
	}
	if len(settings.GeoIP)+len(settings.GeoSite) > 0 {
		rules, err := a.routingData.Resolve(settings.GeoIP, settings.GeoSite)
		if err != nil {
			return nil, errors.New("Routing data could not be loaded. Synchronize GeoIP and GeoSite in Routing settings.")
		}
		for _, value := range rules.Prefixes {
			prefix, err := netip.ParsePrefix(value)
			if err != nil {
				return nil, err
			}
			if prefix.Addr().Is4() {
				prefixes = append(prefixes, prefix.String())
			}
		}
		for _, domain := range rules.Domains {
			domains = append(domains, routingpolicy.Domain{Type: domain.Type, Value: domain.Value})
		}
	}
	policy := routingpolicy.Policy{Mode: settings.Mode, Prefixes: prefixes, Domains: domains, LocalDirect: settings.LocalDirect}
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(policy)
	if err != nil {
		return nil, err
	}
	if len(raw) > routingpolicy.MaxPolicyBytes {
		return nil, errors.New("Resolved routing policy exceeds 32 MiB. Select fewer GeoIP or GeoSite groups.")
	}
	path := filepath.Join(directory, "routing.json")
	if err := writePrivate(path, raw); err != nil {
		return nil, err
	}
	return []string{"-routing-file", path}, nil
}

func (a *App) routingUpdates() {
	a.updateRoutingIfNeeded()
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-ticker.C:
			a.updateRoutingIfNeeded()
		}
	}
}

func (a *App) updateRoutingIfNeeded() {
	a.mu.Lock()
	enabled := a.preferences.RoutingAutoUpdate && !a.closing
	needed := slices.ContainsFunc(a.profiles.Profiles, func(p Profile) bool {
		return p.Routing.Mode != "" && len(p.Routing.GeoIP)+len(p.Routing.GeoSite) > 0
	})
	a.mu.Unlock()
	if !enabled {
		return
	}
	a.routingMu.Lock()
	defer a.routingMu.Unlock()
	catalog, err := a.routingData.Catalog()
	if errors.Is(err, os.ErrNotExist) && !needed {
		return
	}
	if err == nil && time.Since(catalog.UpdatedAt) < time.Duration(defaults.RoutingUpdateSeconds)*time.Second {
		return
	}
	if _, err := a.routingData.Update(a.ctx); err != nil && a.ctx.Err() == nil {
		a.appendLog("Routing data update failed. The last downloaded databases are retained.")
	}
}
