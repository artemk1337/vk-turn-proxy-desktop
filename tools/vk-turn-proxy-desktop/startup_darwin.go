//go:build darwin

// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
)

const macLaunchAgentLabel = "io.github.artemk1337.vkturnproxy"

type macPlistNode struct {
	XMLName xml.Name
	Text    string   `xml:",chardata"`
	Strings []string `xml:"string"`
}

func macAutostartPath() (string, error) {
	home, err := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", macLaunchAgentLabel+".plist"), err
}

func macLaunchAgent(executable string) ([]byte, error) {
	var escaped bytes.Buffer
	if err := xml.EscapeText(&escaped, []byte(executable)); err != nil {
		return nil, err
	}
	return []byte(fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>%s</string>
<key>ProgramArguments</key><array><string>%s</string><string>--autostart</string></array>
<key>RunAtLoad</key><true/>
</dict></plist>
`, macLaunchAgentLabel, escaped.String())), nil
}

func macLaunchAgentMatches(data []byte, executable string) (bool, error) {
	var agent struct {
		Dict struct {
			Nodes []macPlistNode `xml:",any"`
		} `xml:"dict"`
	}
	if err := xml.Unmarshal(data, &agent); err != nil {
		return false, err
	}
	var label string
	var arguments []string
	var enabled bool
	for i := 0; i+1 < len(agent.Dict.Nodes); i += 2 {
		key, value := agent.Dict.Nodes[i], agent.Dict.Nodes[i+1]
		if key.XMLName.Local != "key" {
			return false, nil
		}
		switch key.Text {
		case "Label":
			label = value.Text
		case "ProgramArguments":
			arguments = value.Strings
		case "RunAtLoad":
			enabled = value.XMLName.Local == "true"
		}
	}
	return label == macLaunchAgentLabel && slices.Equal(arguments, []string{executable, "--autostart"}) && enabled, nil
}

func autostartEnabled(executable string) (bool, error) {
	path, err := macAutostartPath()
	if err != nil {
		return false, err
	}
	data, err := readSecret(path, 64<<10)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return macLaunchAgentMatches(data, executable)
}

func setAutostart(enabled bool, executable string) error {
	path, err := macAutostartPath()
	if err != nil {
		return err
	}
	if !enabled {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	data, err := macLaunchAgent(executable)
	if err != nil {
		return err
	}
	return writePrivate(path, data)
}
