// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	desktopsettings "github.com/artemk1337/vk-turn-proxy-desktop/pkg/desktop-settings"
	routingdata "github.com/artemk1337/vk-turn-proxy-desktop/pkg/routing-data"
	vkauth "github.com/artemk1337/vk-turn-proxy-desktop/pkg/vk-auth"
)

type State struct {
	Preferences Preferences              `json:"preferences"`
	Defaults    desktopsettings.Settings `json:"defaults"`
	Profiles    []Profile                `json:"profiles"`
	ActiveID    string                   `json:"activeID"`
	Connection  ConnectionState          `json:"connection"`
	VK          VKState                  `json:"vk"`
	Error       string                   `json:"error"`
}

type ConnectionState struct {
	State                 string  `json:"state"`
	TrafficBytesPerSecond float64 `json:"trafficBytesPerSecond"`
}
type VKState struct {
	Status   string `json:"status"`
	Checking bool   `json:"checking"`
}

type App struct {
	mu            sync.Mutex
	routingMu     sync.Mutex
	routingData   *routingdata.Store
	root          string
	backend       string
	preferences   Preferences
	profiles      profileStore
	cookies       string
	vk            VKState
	generation    uint64
	connection    string
	traffic       trafficSample
	session       *backendSession
	logs          Logs
	logBytes      int
	diskLogs      diskLog
	lastError     string
	ctx           context.Context
	cancel        context.CancelFunc
	quit          func()
	closing       bool
	frontendReady chan struct{}
	frontendOnce  sync.Once
	loginRunning  bool
}

func newApp(root, backend string, quit func()) (*App, error) {
	if err := secureDirectory(root); err != nil {
		return nil, err
	}
	prefs, err := loadPreferences(root)
	warning := err
	diskLogs := diskLog{root: filepath.Join(root, "logs")}
	if err := diskLogs.prepare(time.Now()); err != nil {
		warning = errors.Join(warning, errors.New("Connection logs could not be saved to disk."))
	}
	profiles, err := loadProfiles(root)
	if err != nil {
		warning = errors.Join(warning, errors.New("Saved profiles could not be loaded. Import your profiles again."))
		profiles = profileStore{Profiles: []Profile{}}
	}
	_, authName := storeNames()
	cookies, err := readSecret(filepath.Join(root, authName), maxInput)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		warning = errors.Join(warning, errors.New("VK session could not be loaded. Sign in again."))
		cookies = nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	a := &App{root: root, backend: backend, preferences: prefs, profiles: profiles, routingData: routingdata.NewStore(filepath.Join(root, "routing-data")),
		cookies: string(cookies), vk: VKState{Status: "unknown"}, connection: "disconnected",
		logs: Logs{Lines: []string{}}, diskLogs: diskLog{root: diskLogs.root, day: diskLogs.day}, ctx: ctx, cancel: cancel, quit: quit, frontendReady: make(chan struct{})}
	a.diskLogs.start(a.diskLogs.writeBatch, a.reportDiskLog)
	if warning != nil {
		a.lastError = warning.Error()
	}
	if executable, err := os.Executable(); err == nil {
		if enabled, err := autostartEnabled(executable); err == nil {
			a.preferences.AutoStart = enabled
		}
	}
	return a, nil
}

func (a *App) snapshot() State {
	vk := a.vk
	if a.cookies == "" {
		vk = VKState{Status: "signed_out"}
	}
	traffic := 0.0
	if a.connection == "connected" {
		traffic = a.traffic.rate(time.Now())
	}
	profiles := slices.Clone(a.profiles.Profiles)
	for i := range profiles {
		profiles[i].Routing = cloneRouting(profiles[i].Routing)
	}
	return State{Preferences: a.preferences, Defaults: defaults, Profiles: profiles,
		ActiveID: a.profiles.ActiveID, Connection: ConnectionState{State: a.connection, TrafficBytesPerSecond: traffic}, VK: vk, Error: a.lastError}
}

func (a *App) GetState() State {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.snapshot()
}

func (a *App) FrontendReady() { a.frontendOnce.Do(func() { close(a.frontendReady) }) }

func (a *App) recordError(err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.lastError = err.Error()
}

func (a *App) SavePreferences(p Preferences) (State, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	p = normalizePreferences(p)
	exe := ""
	if p.AutoStart != a.preferences.AutoStart {
		var err error
		exe, err = os.Executable()
		if err != nil {
			return a.snapshot(), err
		}
		if err := setAutostart(p.AutoStart, exe); err != nil {
			return a.snapshot(), err
		}
	}
	raw, err := json.Marshal(p)
	if err == nil {
		err = writePrivate(filepath.Join(a.root, "preferences.json"), raw)
	}
	if err != nil {
		if exe != "" {
			err = errors.Join(err, setAutostart(a.preferences.AutoStart, exe))
		}
		return a.snapshot(), err
	}
	a.preferences = p
	a.trimLogs()
	a.logs.Revision++
	return a.snapshot(), nil
}

func (a *App) SaveProfile(p Profile) (State, error) {
	p.Routing = cloneRouting(p.Routing)
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.session != nil {
		return a.snapshot(), errors.New("Disconnect before changing profiles.")
	}
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" {
		return a.snapshot(), errors.New("Enter a profile name.")
	}
	if len(p.Name) > 256 || len(p.Endpoint) > 4096 || len(p.ServerName) > 4096 {
		return a.snapshot(), errors.New("Profile field exceeds the supported size.")
	}
	if err := a.validateProfile(p); err != nil {
		return a.snapshot(), err
	}
	if p.ID == "" {
		var err error
		p.ID, err = newProfileID()
		if err != nil {
			return a.snapshot(), err
		}
	}
	store := profileStore{Profiles: slices.Clone(a.profiles.Profiles), ActiveID: p.ID}
	index := slices.IndexFunc(store.Profiles, func(item Profile) bool { return item.ID == p.ID })
	if index < 0 {
		store.Profiles = append(store.Profiles, p)
	} else {
		store.Profiles[index] = p
	}
	if err := saveProfiles(a.root, store); err != nil {
		return a.snapshot(), err
	}
	a.profiles = store
	return a.snapshot(), nil
}

func (a *App) DeleteProfile(id string) (State, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.session != nil {
		return a.snapshot(), errors.New("Disconnect before changing profiles.")
	}
	index := slices.IndexFunc(a.profiles.Profiles, func(p Profile) bool { return p.ID == id })
	if index < 0 {
		return a.snapshot(), errors.New("Selected profile does not exist.")
	}
	store := profileStore{Profiles: slices.Delete(slices.Clone(a.profiles.Profiles), index, index+1), ActiveID: a.profiles.ActiveID}
	if store.ActiveID == id {
		store.ActiveID = ""
		if len(store.Profiles) > 0 {
			store.ActiveID = store.Profiles[0].ID
		}
	}
	if err := saveProfiles(a.root, store); err != nil {
		return a.snapshot(), err
	}
	a.profiles = store
	return a.snapshot(), nil
}

func (a *App) SelectProfile(id string) (State, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.session != nil {
		return a.snapshot(), errors.New("Disconnect before changing profiles.")
	}
	if !slices.ContainsFunc(a.profiles.Profiles, func(p Profile) bool { return p.ID == id }) {
		return a.snapshot(), errors.New("Selected profile does not exist.")
	}
	store := a.profiles
	store.ActiveID = id
	if err := saveProfiles(a.root, store); err != nil {
		return a.snapshot(), err
	}
	a.profiles = store
	return a.snapshot(), nil
}

func (a *App) ImportCookies(text string) (State, error) {
	if len(text) > maxInput {
		return a.GetState(), errors.New("The input is larger than 4 MiB.")
	}
	cookies, err := vkauth.ParseCookieInput(strings.NewReader(text))
	if err == nil {
		_, _, err = vkauth.CookieHeader(cookies, time.Now())
	}
	if err != nil {
		return a.GetState(), errors.New("VK cookies are invalid or expired.")
	}
	a.mu.Lock()
	if a.closing {
		a.mu.Unlock()
		return a.GetState(), errors.New("The application is closing.")
	}
	_, name := storeNames()
	if err := writeSecret(filepath.Join(a.root, name), []byte(text)); err != nil {
		state := a.snapshot()
		a.mu.Unlock()
		return state, err
	}
	a.cookies = text
	a.generation++
	a.vk = VKState{Status: "unknown"}
	state := a.snapshot()
	a.mu.Unlock()
	a.checkVK()
	return state, nil
}

func (a *App) LoginVK() (State, error) {
	a.mu.Lock()
	if a.loginRunning || a.closing {
		state := a.snapshot()
		a.mu.Unlock()
		return state, errors.New("VK login is already open or the application is closing.")
	}
	a.loginRunning = true
	language := a.preferences.Language
	a.mu.Unlock()
	defer func() { a.mu.Lock(); a.loginRunning = false; a.mu.Unlock() }()
	text, err := loginVK(a.ctx, language)
	if err != nil {
		return a.GetState(), err
	}
	if text == "" {
		return a.GetState(), nil
	}
	return a.ImportCookies(text)
}

func (a *App) LogoutVK() (State, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	_, name := storeNames()
	if err := os.Remove(filepath.Join(a.root, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return a.snapshot(), err
	}
	a.cookies = ""
	a.generation++
	a.vk = VKState{Status: "signed_out"}
	return a.snapshot(), nil
}

func (a *App) checkVK() {
	a.mu.Lock()
	if a.cookies == "" || a.vk.Checking || a.closing {
		a.mu.Unlock()
		return
	}
	text, generation := a.cookies, a.generation
	a.vk.Checking = true
	a.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(a.ctx, 20*time.Second)
		defer cancel()
		status := vkauth.Check(ctx, text)
		a.mu.Lock()
		defer a.mu.Unlock()
		if generation == a.generation {
			a.vk = VKState{Status: string(status)}
		}
	}()
}

func (a *App) startChecks() {
	a.checkVK()
	go a.routingUpdates()
	go func() {
		ticker := time.NewTicker(time.Duration(defaults.VKCheckSeconds) * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-a.ctx.Done():
				return
			case <-ticker.C:
				a.checkVK()
			}
		}
	}()
}

func (a *App) Exit() {
	a.mu.Lock()
	if a.closing {
		a.mu.Unlock()
		return
	}
	a.closing = true
	session := a.session
	if session != nil {
		if err := writePrivate(session.stopFile, nil); err != nil {
			a.lastError = "Could not request disconnect. Close the application after disconnecting."
			a.closing = false
			a.mu.Unlock()
			return
		}
		a.connection = "disconnecting"
	}
	a.cancel()
	a.mu.Unlock()
	if session != nil {
		<-session.done
	}
	a.diskLogs.close()
	a.quit()
}

func (a *App) reportDiskLog(message string) {
	a.mu.Lock()
	a.lastError = message
	a.mu.Unlock()
}

func (a *App) shutdown() {
	a.mu.Lock()
	a.closing = true
	session := a.session
	a.mu.Unlock()
	if session != nil {
		_ = writePrivate(session.stopFile, nil)
	}
	a.cancel()
	if session != nil {
		<-session.done
	}
	a.diskLogs.close()
}
