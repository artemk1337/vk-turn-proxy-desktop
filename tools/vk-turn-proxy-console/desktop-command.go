// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	vkauth "github.com/artemk1337/vk-turn-proxy-desktop/pkg/vk-auth"
)

func validateDesktopCommandFlags(o *options) error {
	commands := 0
	for _, enabled := range []bool{o.validateVKCookies, o.checkVKSession, o.validateProfile} {
		if enabled {
			commands++
		}
	}
	if commands == 0 {
		return nil
	}
	if commands != 1 {
		return errors.New("select one desktop validation command")
	}
	if !o.validateProfile && o.cookieFile == "" {
		return errors.New("VK validation commands require -vk-cookie-file")
	}
	for name := range o.set {
		allowed := name == "validate-vk-cookies" || name == "check-vk-session" || name == "vk-cookie-file"
		if o.validateProfile {
			switch name {
			case "validate-profile", "config", "wg-config", "vk-link-file", "peer", "server-transport", "server", "conns", "mtu", "turn-transport":
				allowed = true
			default:
				allowed = false
			}
		}
		if !allowed {
			return fmt.Errorf("-%s cannot be combined with this validation command", name)
		}
	}
	return nil
}

// Desktop commands run as the desktop user, before logs, locks or network setup.
// Their output contains bounded status values and never profile or cookie data.
func runDesktopCommand(ctx context.Context, o *options, output io.Writer) error {
	status := vkauth.Valid
	if o.validateProfile {
		if err := validateDesktopProfile(o); err != nil {
			return err
		}
	} else {
		raw, err := readManualInput(o.cookieFile)
		if err != nil {
			return errors.New("VK cookie file could not be read or exceeds 4 MiB")
		}
		if o.checkVKSession {
			status = vkauth.Check(ctx, string(raw))
		} else {
			cookies, err := vkauth.ParseCookieInput(strings.NewReader(string(raw)))
			if err != nil {
				return errors.New("VK cookies are invalid or expired")
			}
			if _, _, err := vkauth.CookieHeader(cookies, time.Now()); err != nil {
				return errors.New("VK cookies are invalid or expired")
			}
		}
	}
	return json.NewEncoder(output).Encode(struct {
		Status vkauth.Status `json:"status"`
	}{status})
}

func validateDesktopProfile(o *options) error {
	var backup *backupFile
	var err error
	if o.wgConfigPath != "" {
		backup, err = loadWireGuardConfig(o)
	} else {
		var raw []byte
		raw, err = readManualInput(o.configPath)
		if err == nil {
			backup, err = parseProfile(raw)
		}
	}
	if err != nil {
		return errors.New("connection profile could not be read or is invalid")
	}
	server, err := backup.selectedServer(o.serverName)
	if err != nil {
		return errors.New("connection profile has no matching supported server")
	}
	settings := buildSettings(backup, server)
	if _, err := o.applyTo(&settings); err != nil {
		return errors.New("connection profile overrides are invalid")
	}
	if err := settings.validate(); err != nil {
		return errors.New("connection profile has invalid keys, addresses or connection parameters")
	}
	return nil
}
