// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"time"
)

// version is set by release builds with -ldflags "-X main.version=<tag>".
var version = "dev"

const releaseAPI = "https://api.github.com/repos/artemk1337/vk-turn-proxy-desktop/releases/latest"
const releasePage = "https://github.com/artemk1337/vk-turn-proxy-desktop/releases/tag/"
const updateInterval = 24 * time.Hour
const maxReleaseBytes = 1024 * 1024

var releaseVersionPattern = regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

type UpdateState struct {
	DownloadStatus string `json:"downloadStatus"`
	Downloaded     int64  `json:"downloaded"`
	Total          int64  `json:"total"`
	Path           string `json:"path"`
	CurrentVersion string `json:"currentVersion"`
	LatestVersion  string `json:"latestVersion"`
	Status         string `json:"status"`
	URL            string `json:"url"`
}

func parseReleaseVersion(value string) ([3]uint64, bool) {
	var parsed [3]uint64
	parts := releaseVersionPattern.FindStringSubmatch(value)
	if parts == nil {
		return parsed, false
	}
	for index, part := range parts[1:] {
		number, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			return parsed, false
		}
		parsed[index] = number
	}
	return parsed, true
}

func releaseIsNewer(candidate, current string) bool {
	next, valid := parseReleaseVersion(candidate)
	installed, currentValid := parseReleaseVersion(current)
	if !valid || !currentValid {
		return false
	}
	for index := range next {
		if next[index] != installed[index] {
			return next[index] > installed[index]
		}
	}
	return false
}

func initialUpdateState() UpdateState {
	status := "idle"
	if _, valid := parseReleaseVersion(version); !valid {
		status = "development"
	}
	return UpdateState{CurrentVersion: version, Status: status}
}

func fetchLatestRelease(ctx context.Context) (string, error) {
	client := &http.Client{
		Timeout:       20 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return requestLatestRelease(ctx, client, releaseAPI)
}

func requestLatestRelease(ctx context.Context, client *http.Client, endpoint string) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "VK-TURN-Proxy-Desktop")
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", errors.New("Could not check application updates.")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxReleaseBytes+1))
	if err != nil {
		return "", err
	}
	if len(body) > maxReleaseBytes {
		return "", errors.New("Release information is too large.")
	}
	var release struct {
		TagName    string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
	}
	if err := json.Unmarshal(body, &release); err != nil {
		return "", err
	}
	if _, valid := parseReleaseVersion(release.TagName); !valid || release.Draft || release.Prerelease {
		return "", errors.New("The release is not a stable version.")
	}
	return release.TagName, nil
}

func (a *App) CheckForUpdates() State {
	return a.checkForUpdates(true)
}

func (a *App) checkForUpdates(manual bool) State {
	a.mu.Lock()
	if a.closing || a.updates.Status == "checking" || a.updates.DownloadStatus == "downloading" || a.updates.DownloadStatus == "choosing" {
		state := a.snapshot()
		a.mu.Unlock()
		return state
	}
	if _, valid := parseReleaseVersion(version); !valid {
		a.updates = initialUpdateState()
		state := a.snapshot()
		a.mu.Unlock()
		return state
	}
	if !manual && !a.updateAttempt.IsZero() && time.Since(a.updateAttempt) < updateInterval {
		state := a.snapshot()
		a.mu.Unlock()
		return state
	}
	a.updateAttempt = time.Now()
	a.updates.CurrentVersion = version
	a.updates.Status = "checking"
	state := a.snapshot()
	a.mu.Unlock()
	go func() {
		latest, err := a.updateFetch(a.ctx)
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.closing {
			return
		}
		if err != nil {
			a.updates.Status = "error"
			return
		}
		if a.updates.LatestVersion != latest {
			a.updates.DownloadStatus = ""
			a.updates.Path = ""
		}
		a.updates.LatestVersion = latest
		a.updates.URL = ""
		a.updates.Status = "current"
		if releaseIsNewer(latest, a.updates.CurrentVersion) {
			a.updates.Status = "available"
			a.updates.URL = releasePage + latest
		}
	}()
	return state
}

func (a *App) appUpdates() {
	a.checkForUpdates(false)
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-ticker.C:
			a.checkForUpdates(false)
		}
	}
}
