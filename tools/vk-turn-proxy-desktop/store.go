// SPDX-License-Identifier: MIT

package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"

	desktopsettings "github.com/artemk1337/vk-turn-proxy-desktop/pkg/desktop-settings"
	routingpolicy "github.com/artemk1337/vk-turn-proxy-desktop/pkg/routing-policy"
)

const maxInput = 4 << 20
const maxStore = 32 << 20

var defaults = desktopsettings.Defaults()

type Preferences struct {
	Language          string `json:"language"`
	Theme             string `json:"theme"`
	AutoStart         bool   `json:"autoStart"`
	LogLines          int    `json:"logLines"`
	RoutingAutoUpdate bool   `json:"routingAutoUpdate"`
}

type Profile struct {
	ID             string                 `json:"id"`
	Name           string                 `json:"name"`
	Mode           string                 `json:"mode"`
	ProfileText    string                 `json:"profileText"`
	WGConfig       string                 `json:"wgConfig"`
	VKLink         string                 `json:"vkLink"`
	Endpoint       string                 `json:"endpoint"`
	ServerName     string                 `json:"serverName"`
	UseSRTP        bool                   `json:"useSRTP"`
	Routing        routingpolicy.Settings `json:"routing"`
	MTU            int                    `json:"mtu"`
	NumConnections int                    `json:"numConnections"`
	TURNTransport  string                 `json:"turnTransport"`
}

func (p *Profile) UnmarshalJSON(data []byte) error {
	type profileJSON Profile
	value := profileJSON{MTU: defaults.MTU.Default, NumConnections: defaults.Connections.Default, UseSRTP: defaults.DefaultTransport == "srtp", Routing: routingpolicy.Settings{LocalDirect: true}}
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*p = Profile(value)
	return nil
}

type profileStore struct {
	Profiles []Profile `json:"profiles"`
	ActiveID string    `json:"activeID"`
}

func normalizePreferences(p Preferences) Preferences {
	if !slices.Contains(defaults.Languages[:], p.Language) {
		p.Language = defaults.DefaultLanguage
	}
	if !slices.Contains(defaults.Themes[:], p.Theme) {
		p.Theme = defaults.DefaultTheme
	}
	if p.LogLines <= 0 {
		p.LogLines = defaults.LogLines.Default
	}
	p.LogLines = max(defaults.LogLines.Min, min(defaults.LogLines.Max, p.LogLines))
	return p
}

func loadPreferences(root string) (Preferences, error) {
	p := normalizePreferences(Preferences{Language: systemLanguage(), RoutingAutoUpdate: true})
	file, err := os.Open(filepath.Join(root, "preferences.json"))
	if errors.Is(err, os.ErrNotExist) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, (64<<10)+1))
	if err != nil {
		return p, err
	}
	if len(raw) > 64<<10 {
		return p, errors.New("Preferences exceed the supported size.")
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return normalizePreferences(Preferences{Language: systemLanguage(), RoutingAutoUpdate: true}), errors.New("Preferences could not be loaded. Default settings were restored.")
	}
	return normalizePreferences(p), nil
}

func systemLanguage() string {
	if language := nativeLanguage(); language != "" {
		return language
	}
	for _, name := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if value := os.Getenv(name); value != "" {
			if strings.HasPrefix(strings.ToLower(value), "ru") {
				return "ru"
			}
			return defaults.DefaultLanguage
		}
	}
	return defaults.DefaultLanguage
}

func storeNames() (string, string) {
	if runtime.GOOS == "windows" {
		return "profiles.dat", "vk-auth.bin"
	}
	return "profiles.json", "vk-session.txt"
}

func loadProfiles(root string) (profileStore, error) {
	store := profileStore{Profiles: []Profile{}}
	name, _ := storeNames()
	raw, err := readSecret(filepath.Join(root, name), maxStore)
	if errors.Is(err, os.ErrNotExist) {
		return store, nil
	}
	if err != nil {
		return store, err
	}
	if err := json.Unmarshal(raw, &store); err != nil {
		return store, errors.New("Saved profiles could not be loaded.")
	}
	if store.Profiles == nil {
		store.Profiles = []Profile{}
	}
	return store, validateProfiles(store)
}

func validateProfiles(store profileStore) error {
	ids := make(map[string]bool, len(store.Profiles))
	for _, p := range store.Profiles {
		if p.ID == "" || ids[p.ID] || strings.TrimSpace(p.Name) == "" {
			return errors.New("Invalid saved profile.")
		}
		if p.Mode != "link" && p.Mode != "wireguard" {
			return errors.New("Unknown profile type.")
		}
		if err := p.Routing.Validate(); err != nil {
			return err
		}
		ids[p.ID] = true
	}
	if store.ActiveID != "" && !ids[store.ActiveID] {
		return errors.New("Selected profile does not exist.")
	}
	return nil
}

func saveProfiles(root string, store profileStore) error {
	if err := validateProfiles(store); err != nil {
		return err
	}
	raw, err := json.Marshal(store)
	if err != nil {
		return err
	}
	if len(raw) > maxStore {
		return errors.New("Profile storage exceeds 32 MiB.")
	}
	name, _ := storeNames()
	return writeSecret(filepath.Join(root, name), raw)
}

func newProfileID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func profileArguments(p Profile, session string) ([]string, error) {
	if p.TURNTransport != "" && p.TURNTransport != "tcp" && p.TURNTransport != "udp" {
		return nil, errors.New("TURN transport must be TCP or UDP.")
	}
	connections := p.NumConnections
	if connections == 0 {
		connections = defaults.Connections.Default
	}
	if connections < defaults.Connections.Min || connections > defaults.Connections.Max {
		return nil, errors.New("TURN connections must be between 1 and 120.")
	}
	mtu := p.MTU
	if mtu == 0 {
		mtu = defaults.MTU.Default
	}
	if mtu < defaults.MTU.Min || mtu > defaults.MTU.Max {
		return nil, errors.New("MTU must be between 1000 and 1400.")
	}
	turnArgs := []string{"-conns", strconv.Itoa(connections), "-mtu", strconv.Itoa(mtu)}
	if p.TURNTransport != "" {
		turnArgs = append(turnArgs, "-turn-transport", p.TURNTransport)
	}
	raw := strings.TrimSpace(p.ProfileText)
	if p.Mode == "wireguard" {
		raw = strings.TrimSpace(p.WGConfig)
	}
	if raw == "" {
		return nil, errors.New("Add a connection profile first.")
	}
	if len(raw) > maxInput || len(p.VKLink) > maxInput {
		return nil, errors.New("The input is larger than 4 MiB.")
	}
	path := filepath.Join(session, "profile.txt")
	if err := writePrivate(path, []byte(raw)); err != nil {
		return nil, err
	}
	if p.Mode == "link" {
		args := []string{"-config", path}
		if name := strings.TrimSpace(p.ServerName); name != "" {
			args = append(args, "-server", name)
		}
		return append(args, turnArgs...), nil
	}
	if p.Mode != "wireguard" {
		return nil, errors.New("Unknown profile type.")
	}
	if strings.TrimSpace(p.VKLink) == "" {
		return nil, errors.New("Add a WireGuard configuration and VK call link.")
	}
	link := filepath.Join(session, "vk-link.txt")
	if err := writePrivate(link, []byte(p.VKLink)); err != nil {
		return nil, err
	}
	transport := "dtls"
	if p.UseSRTP {
		transport = "srtp"
	}
	args := []string{"-wg-config", path, "-vk-link-file", link, "-server-transport", transport}
	if endpoint := strings.TrimSpace(p.Endpoint); endpoint != "" {
		args = append(args, "-peer", endpoint)
	}
	return append(args, turnArgs...), nil
}
