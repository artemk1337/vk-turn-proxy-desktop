// SPDX-License-Identifier: MIT

package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"

	wgprofile "github.com/artemk1337/vk-turn-proxy-desktop/pkg/wireguard-profile"
)

type savedProfile struct {
	ID          string
	Name        string
	Mode        string
	ProfileText string
	WGConfig    string
	VKLink      string
	Endpoint    string
	ServerName  string
	UseSRTP     bool
}

type profileStore struct {
	Profiles []savedProfile
	ActiveID string
}

func newProfileID() (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(id[:]), nil
}

func (p savedProfile) connectionProfile() ([]byte, error) {
	switch p.Mode {
	case "wireguard":
		return wgprofile.Build(wgprofile.Options{Config: p.WGConfig, VKLink: p.VKLink, PeerAddress: p.Endpoint, UseSrtp: p.UseSRTP})
	case "link":
		text := strings.TrimSpace(p.ProfileText)
		if text == "" {
			return nil, errors.New("Paste a connection link or open a profile first.")
		}
		return []byte(text), nil
	default:
		return nil, errors.New("Unknown profile type.")
	}
}

func (s profileStore) validate() error {
	ids := make(map[string]bool, len(s.Profiles))
	for _, p := range s.Profiles {
		if p.ID == "" || ids[p.ID] || strings.TrimSpace(p.Name) == "" {
			return errors.New("Invalid saved profile.")
		}
		if p.Mode != "link" && p.Mode != "wireguard" {
			return errors.New("Unknown profile type.")
		}
		ids[p.ID] = true
	}
	if s.ActiveID != "" && !ids[s.ActiveID] {
		return errors.New("Selected profile does not exist.")
	}
	return nil
}
