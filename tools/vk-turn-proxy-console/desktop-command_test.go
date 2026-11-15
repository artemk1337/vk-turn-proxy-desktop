// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDesktopCommandFlags(t *testing.T) {
	for _, args := range [][]string{
		{"-validate-vk-cookies"},
		{"-check-vk-session"},
		{"-validate-vk-cookies", "-check-vk-session", "-vk-cookie-file", "private"},
		{"-validate-vk-cookies", "-vk-cookie-file", "private", "-log", "public.log"},
		{"-check-vk-session", "-vk-cookie-file", "private", "-cleanup"},
		{"-validate-profile", "-wizard"},
		{"-validate-profile", "-vk-cookie-file", "private"},
		{"-validate-profile", "-config", "private", "-version"},
	} {
		if _, err := parseFlags(args); err == nil {
			t.Fatalf("accepted incompatible validation flags %v", args)
		}
	}
}

func TestDesktopCookieValidationKeepsSecretsOutOfOutput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookies.txt")
	for _, input := range []string{
		"remixsid=secret-session; p=secret-login",
		"# Netscape HTTP Cookie File\n.vk.ru\tTRUE\t/\tTRUE\t0\tremixsid\tsecret-session\n.login.vk.ru\tTRUE\t/\tTRUE\t0\tp\tsecret-login\n",
	} {
		writeFile(t, path, input)
		o, err := parseFlags([]string{"-validate-vk-cookies", "-vk-cookie-file", path})
		if err != nil {
			t.Fatal(err)
		}
		var output bytes.Buffer
		if err := runDesktopCommand(context.Background(), o, &output); err != nil {
			t.Fatal(err)
		}
		if got := output.String(); got != "{\"status\":\"valid\"}\n" {
			t.Fatalf("unexpected validation output %q", got)
		}
	}
	for _, input := range []string{"remixsid=secret-missing-p", "secret-malformed-input", strings.Repeat("s", (4<<20)+1)} {
		writeFile(t, path, input)
		o, _ := parseFlags([]string{"-validate-vk-cookies", "-vk-cookie-file", path})
		var output bytes.Buffer
		err := runDesktopCommand(context.Background(), o, &output)
		if err == nil || strings.Contains(err.Error(), "secret") || output.Len() != 0 {
			t.Fatal("invalid cookies must fail without exposing input")
		}
	}
}

func TestDesktopProfileValidationUsesSharedParsers(t *testing.T) {
	root := t.TempDir()
	config := filepath.Join(root, "profile.conf")
	link := filepath.Join(root, "call.txt")
	writeFile(t, config, fmt.Sprintf("[Interface]\nPrivateKey = %s\nAddress = 10.66.0.2/32\n[Peer]\nPublicKey = %s\nEndpoint = relay.example:56000\nAllowedIPs = 0.0.0.0/0\n", testKey(1), testKey(2)))
	writeFile(t, link, "https://vk.ru/call/join/private")
	for _, args := range [][]string{
		{"-validate-profile", "-config", writeBackup(t, globals(srtpServer("Home"))), "-server", "Home", "-conns", "30", "-mtu", "1280"},
		{"-validate-profile", "-wg-config", config, "-vk-link-file", link, "-peer", "override.example:56000", "-server-transport", "dtls", "-conns", "30", "-mtu", "1280", "-turn-transport", "udp"},
	} {
		o, err := parseFlags(args)
		if err != nil {
			t.Fatal(err)
		}
		var output bytes.Buffer
		if err := runDesktopCommand(context.Background(), o, &output); err != nil {
			t.Fatal(err)
		}
		if output.String() != "{\"status\":\"valid\"}\n" {
			t.Fatal("profile validator must return only status")
		}
	}
	writeFile(t, config, "[Interface]\nPrivateKey = secret-invalid-key\nPostUp = secret-command\n")
	o, _ := parseFlags([]string{"-validate-profile", "-wg-config", config, "-vk-link-file", link})
	var output bytes.Buffer
	err := runDesktopCommand(context.Background(), o, &output)
	if err == nil || strings.Contains(err.Error(), "secret") || output.Len() != 0 {
		t.Fatal("unsupported WireGuard settings must fail without exposing input")
	}
}

func TestDesktopSessionCheckRejectsMalformedCookies(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookies.txt")
	writeFile(t, path, "remixsid=secret-missing-login-cookie")
	o, err := parseFlags([]string{"-check-vk-session", "-vk-cookie-file", path})
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := runDesktopCommand(context.Background(), o, &output); err != nil {
		t.Fatal(err)
	}
	if output.String() != "{\"status\":\"rejected\"}\n" {
		t.Fatal("invalid session must be rejected without exposing cookies")
	}
}

func TestDesktopValidationRunsBeforeNetworkSetup(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "cookies.txt")
	writeFile(t, path, "remixsid=secret; p=secret")
	if code := realMain([]string{"-validate-vk-cookies", "-vk-cookie-file", path}); code != 0 {
		t.Fatal("cookie validation required a profile or privileged network access")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if code := realMain([]string{"-check-vk-session", "-vk-cookie-file", path}); code != 2 {
		t.Fatal("missing cookie file must return an input error")
	}
}
