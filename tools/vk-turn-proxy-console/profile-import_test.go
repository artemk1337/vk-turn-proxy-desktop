// SPDX-License-Identifier: MIT

package main

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func connectionJSON(t *testing.T, fields map[string]any) []byte {
	t.Helper()
	fields["vkLink"] = "https://vk.ru/call/join/test"
	raw, err := json.Marshal(map[string]any{"version": 1, "type": "connection", "settings": fields})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestConnectionImportFormats(t *testing.T) {
	raw := connectionJSON(t, srtpServer("Home"))
	encodings := []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding}
	forms := []string{string(raw), "\ufeff" + string(raw)}
	for _, encoding := range encodings {
		b64 := encoding.EncodeToString(raw)
		forms = append(forms, b64, "vkturnproxy://import?data="+b64,
			"vkturnproxy://import?data="+url.QueryEscape(b64), "vkturnproxy:?data="+b64)
	}
	for i, form := range forms {
		t.Run(string(rune('A'+i)), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "profile.txt")
			if err := os.WriteFile(path, []byte("  \n"+form+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			b, err := loadBackup(path)
			if err != nil {
				t.Fatal(err)
			}
			s, err := b.selectedServer("")
			if err != nil {
				t.Fatal(err)
			}
			st := buildSettings(b, s)
			if err := st.validate(); err != nil {
				t.Fatal(err)
			}
			if !st.UseSrtp || !st.UseDTLS || st.PrivateKey != testKey(1) || st.VKLinks[0] != "https://vk.ru/call/join/test" {
				t.Fatal("connection settings changed during import")
			}
		})
	}
}

func TestConnectionURLPreservesLiteralPlus(t *testing.T) {
	fields := srtpServer("Home")
	fields["serverName"] = "~~~"
	raw := connectionJSON(t, fields)
	b64 := base64.StdEncoding.EncodeToString(raw)
	// Vary harmless whitespace until a standard base64 '+' is present.
	for !strings.Contains(b64, "+") {
		fields["serverName"] = fields["serverName"].(string) + "~"
		raw = connectionJSON(t, fields)
		b64 = base64.StdEncoding.EncodeToString(raw)
		if len(raw) > 2000 {
			t.Fatal("test fixture did not produce a plus")
		}
	}
	if _, err := parseProfile([]byte("vkturnproxy://import?data=" + b64)); err != nil {
		t.Fatal(err)
	}
}

func TestConnectionRejectsMalformedProfilesWithoutEchoingSecrets(t *testing.T) {
	good := base64.StdEncoding.EncodeToString(connectionJSON(t, srtpServer("Home")))
	cases := []string{
		"vkturnproxy://other?data=" + good,
		"vkturnproxy://import?data=" + good + "&data=" + good,
		"vkturnproxy://import?data=%XX", "vkturnproxy://import", "not-base64-secret",
		`{"version":2,"type":"connection","settings":{}}`,
		`{"version":1,"type":"other","settings":{}}`,
		`{"version":1,"type":"connection","settings":{"privateKey":"secret","servers":[{}]}}`,
		"vkturnproxy://import?data=" + base64.StdEncoding.EncodeToString([]byte(`{"version":1,"type":"full","settings":{}}`)),
	}
	for _, input := range cases {
		_, err := parseProfile([]byte(input))
		if err == nil {
			t.Fatal("malformed profile accepted")
		}
		if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), good) {
			t.Fatal("profile content leaked through an error")
		}
	}
}

func TestConnectionSupportsLegacyDTLSAndRejectsOtherTransports(t *testing.T) {
	fields := srtpServer("Legacy")
	fields["useSrtp"] = false
	b, err := parseProfile(connectionJSON(t, fields))
	if err != nil {
		t.Fatal(err)
	}
	s, err := b.selectedServer("")
	if err != nil {
		t.Fatal(err)
	}
	st := buildSettings(b, s)
	if st.UseSrtp || !st.UseDTLS {
		t.Fatal("legacy transport changed")
	}
	for _, mode := range []string{"useWrapA", "useWrapS", "useCsqtt", "useWrap"} {
		fields := srtpServer("Unsupported")
		fields["useSrtp"] = false
		fields[mode] = true
		b, err := parseProfile(connectionJSON(t, fields))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := b.selectedServer(""); err == nil {
			t.Fatalf("%s accepted", mode)
		}
	}
}

func TestActiveBackupServerIsSelectedAndExplicitChoiceWins(t *testing.T) {
	globals := globals(srtpServer("First"), srtpServer("Active"))
	globals["activeServer"] = "Active"
	b, err := loadBackup(writeBackup(t, globals))
	if err != nil {
		t.Fatal(err)
	}
	if s, err := b.selectedServer(""); err != nil || s.Name != "Active" {
		t.Fatalf("active server: %q, %v", s.Name, err)
	}
	if s, err := b.selectedServer("First"); err != nil || s.Name != "First" {
		t.Fatalf("explicit server: %q, %v", s.Name, err)
	}
	b.Settings.Servers[1].UseWrapA = new(true)
	if _, err := b.selectedServer(""); err == nil {
		t.Fatal("unsupported active server silently replaced")
	}
}

func TestConnectionTransportFlagsFollowIOSPrecedence(t *testing.T) {
	fields := srtpServer("Home")
	fields["useWrap"] = true
	b, err := parseProfile(connectionJSON(t, fields))
	if err != nil {
		t.Fatal(err)
	}
	s, err := b.selectedServer("")
	if err != nil {
		t.Fatal(err)
	}
	if !s.UseSrtp || s.UseWrap {
		t.Fatal("SRTP must take precedence over legacy WRAP")
	}
	delete(fields, "useSrtp")
	fields["useWrap"] = false
	b, err = parseProfile(connectionJSON(t, fields))
	if err != nil {
		t.Fatal(err)
	}
	s, err = b.selectedServer("")
	if err != nil {
		t.Fatal(err)
	}
	if s.UseSrtp || !s.UseDTLS {
		t.Fatal("an explicit legacy mode flag must replace the SRTP default")
	}
}
