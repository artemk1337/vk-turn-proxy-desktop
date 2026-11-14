// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"errors"
	"os"
)

type preferences struct {
	Language  string `json:"language"`
	Theme     string `json:"theme"`
	AutoStart bool   `json:"autoStart"`
}

func defaultPreferences(language string) preferences {
	if language != "ru" {
		language = "en"
	}
	return preferences{Language: language, Theme: "light"}
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
	if value.Language != "en" && value.Language != "ru" {
		value.Language = defaultPreferences(language).Language
	}
	if value.Theme != "light" && value.Theme != "dark" {
		value.Theme = "light"
	}
	return value, nil
}

func savePreferences(path string, value preferences) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	// Preferences contain presentation choices only. Profile keys stay in the
	// existing private session directory and never enter this file.
	return os.WriteFile(path, append(data, '\n'), 0600)
}
