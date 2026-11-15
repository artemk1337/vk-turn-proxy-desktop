// SPDX-License-Identifier: MIT

package desktopsettings

import (
	_ "embed"
	"encoding/json"
)

//go:embed settings.json
var settingsJSON []byte

type Settings struct {
	Languages        [2]string `json:"languages"`
	Themes           [2]string `json:"themes"`
	DefaultLanguage  string    `json:"defaultLanguage"`
	DefaultTheme     string    `json:"defaultTheme"`
	LogLines         LogLines  `json:"logLines"`
	LogMaxBytes      int       `json:"logMaxBytes"`
	VKCheckSeconds   int       `json:"vkCheckSeconds"`
	DefaultTransport string    `json:"defaultTransport"`
}

type LogLines struct {
	Default int `json:"default"`
	Min     int `json:"min"`
	Max     int `json:"max"`
}

var defaults = readSettings()

func readSettings() Settings {
	var value Settings
	if err := json.Unmarshal(settingsJSON, &value); err != nil {
		panic(err)
	}
	return value
}

// Defaults returns the contract embedded from the same file shipped with GTK.
func Defaults() Settings {
	return defaults
}
