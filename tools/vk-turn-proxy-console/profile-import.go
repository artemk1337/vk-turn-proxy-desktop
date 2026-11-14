// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// parseProfile accepts the iOS app's full backup, connection JSON, connection
// URL, or the bare base64 payload copied from a connection URL.
func parseProfile(raw []byte) (*backupFile, error) {
	text := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(raw)), "\ufeff"))
	encoded := !strings.HasPrefix(text, "{")
	if strings.HasPrefix(strings.ToLower(text), "vkturnproxy:") {
		decoded, err := decodeConnectionURL(text)
		if err != nil {
			return nil, err
		}
		raw = decoded
	} else if !strings.HasPrefix(text, "{") {
		decoded, err := decodeConnectionBase64(text)
		if err != nil {
			return nil, err
		}
		raw = decoded
	} else {
		raw = []byte(text)
	}
	var b backupFile
	if err := json.Unmarshal(raw, &b); err != nil {
		return nil, errors.New("profile contains invalid JSON")
	}
	if b.Version != 1 {
		return nil, fmt.Errorf("profile version %d is unsupported, expected version 1", b.Version)
	}
	if encoded && b.Type != "connection" {
		return nil, errors.New("encoded payload must contain a connection profile")
	}
	if b.Type != "full" && b.Type != "connection" {
		return nil, errors.New("profile type must be full or connection")
	}
	if b.Type == "connection" {
		if len(b.Settings.Servers) > 0 {
			return nil, errors.New("a connection profile must contain one server in settings")
		}
		s := &b.Settings.backupServer
		// The iOS importer treats the five transport flags as one enum.
		// An explicit flag set replaces the defaults for the entire set.
		if s.UseSrtp != nil || s.UseWrap != nil || s.UseWrapA != nil || s.UseWrapS != nil || s.UseCsqtt != nil {
			useCsqtt := boolOr(s.UseCsqtt, false)
			useWrapS := !useCsqtt && boolOr(s.UseWrapS, false)
			useWrapA := !useCsqtt && !useWrapS && boolOr(s.UseWrapA, false)
			useSrtp := !useCsqtt && !useWrapS && !useWrapA && boolOr(s.UseSrtp, false)
			useWrap := !useCsqtt && !useWrapS && !useWrapA && !useSrtp && boolOr(s.UseWrap, false)
			s.UseCsqtt, s.UseWrapS, s.UseWrapA = &useCsqtt, &useWrapS, &useWrapA
			s.UseSrtp, s.UseWrap = &useSrtp, &useWrap
		}
		if s.ServerName == "" {
			s.ServerName = "Imported profile"
		}
		b.Settings.Servers = []backupServer{*s}
	}
	return &b, nil
}

func decodeConnectionURL(text string) ([]byte, error) {
	u, err := url.Parse(text)
	if err != nil || !strings.EqualFold(u.Scheme, "vkturnproxy") {
		return nil, errors.New("invalid vkturnproxy connection URL")
	}
	if u.User != nil || u.Port() != "" || (u.Host != "" && !strings.EqualFold(u.Host, "import")) || u.Path != "" || u.Fragment != "" {
		return nil, errors.New("connection URL must use vkturnproxy://import?data=...")
	}
	// Decode percent escapes without form decoding. Literal '+' belongs to
	// standard base64 and must survive the query parameter unchanged.
	var payload string
	found := false
	for _, item := range strings.Split(u.RawQuery, "&") {
		key, value, _ := strings.Cut(item, "=")
		key, err = url.PathUnescape(key)
		if err != nil {
			return nil, errors.New("connection URL contains invalid percent escapes")
		}
		if key != "data" {
			continue
		}
		if found {
			return nil, errors.New("connection URL contains multiple data parameters")
		}
		found = true
		payload, err = url.PathUnescape(value)
		if err != nil {
			return nil, errors.New("connection URL contains invalid percent escapes")
		}
	}
	if payload == "" {
		return nil, errors.New("connection URL is missing its data parameter")
	}
	raw, err := decodeConnectionBase64(payload)
	if err != nil {
		return nil, err
	}
	var header struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(raw, &header) != nil || header.Type != "connection" {
		return nil, errors.New("connection URL must contain a connection profile")
	}
	return raw, nil
}

func decodeConnectionBase64(text string) ([]byte, error) {
	normalized := strings.NewReplacer("-", "+", "_", "/").Replace(text)
	encoding := base64.RawStdEncoding
	if strings.Contains(normalized, "=") {
		encoding = base64.StdEncoding
	}
	raw, err := encoding.DecodeString(normalized)
	if err != nil || len(bytes.TrimSpace(raw)) == 0 {
		return nil, errors.New("connection profile contains invalid base64")
	}
	return raw, nil
}

// selectedServer follows an explicitly requested name, then the active server
// saved by iOS, then the first compatible server for older backups.
func (b *backupFile) selectedServer(name string) (server, error) {
	if name == "" && b.Settings.ActiveServer != nil {
		active := *b.Settings.ActiveServer
		for _, s := range b.servers() {
			if s.Name == active {
				name = active
				break
			}
		}
	}
	return selectServer(b.servers(), name)
}
