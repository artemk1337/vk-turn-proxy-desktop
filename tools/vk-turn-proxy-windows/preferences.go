// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"errors"
	"os"
	"slices"
)

type preferences struct {
	Language  string `json:"language"`
	Theme     string `json:"theme"`
	AutoStart bool   `json:"autoStart"`
	LogLines  int    `json:"logLines"`
}

func defaultPreferences(language string) preferences {
	if !slices.Contains(desktopDefaults.Languages[:], language) {
		language = desktopDefaults.DefaultLanguage
	}
	return preferences{Language: language, Theme: desktopDefaults.DefaultTheme, LogLines: defaultLogLines}
}

func loadPreferences(path, language string) (preferences, error) {
	value := defaultPreferences(language)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return value, nil
	}
	if err != nil {
		return value, err
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return defaultPreferences(language), err
	}
	if !slices.Contains(desktopDefaults.Languages[:], value.Language) {
		value.Language = defaultPreferences(language).Language
	}
	if !slices.Contains(desktopDefaults.Themes[:], value.Theme) {
		value.Theme = desktopDefaults.DefaultTheme
	}
	value.LogLines = normalizeLogLimit(value.LogLines)
	return value, nil
}

func savePreferences(path string, value preferences) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	// Preferences contain presentation choices only. Profile keys and cookies
	// use separate encrypted storage.
	return os.WriteFile(path, append(data, '\n'), 0600)
}
