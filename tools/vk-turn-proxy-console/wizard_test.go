package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func wizardProfile(t *testing.T) string {
	t.Helper()
	settings := srtpServer("Home")
	settings["vkLink"] = "https://vk.ru/call/join/test"
	raw, err := json.Marshal(map[string]any{"version": 1, "type": "connection", "settings": settings})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func wizardOptions(t *testing.T, flags ...string) *options {
	t.Helper()
	o, err := parseFlags(append([]string{"-wizard", "-lang", "en"}, flags...))
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func TestWizardImportsLinkWithoutPrintingItsSecrets(t *testing.T) {
	raw := wizardProfile(t)
	link := "vkturnproxy://import?data=" + url.QueryEscape(base64.StdEncoding.EncodeToString([]byte(raw)))
	var output bytes.Buffer
	o := wizardOptions(t)
	profile, err := runSetupWizard(strings.NewReader("1\n"+link+"\n\n\n"), &output, o)
	if err != nil {
		t.Fatal(err)
	}
	server, err := profile.selectedServer("")
	if err != nil || server.Name != "Home" {
		t.Fatal("profile link did not reach the normal settings parser")
	}
	if strings.Contains(output.String(), testKey(1)) || strings.Contains(output.String(), link) {
		t.Fatal("wizard printed imported secrets")
	}
	if !o.blockIPv6 {
		t.Fatal("wizard did not enable IPv6 blocking for its IPv4 tunnel")
	}
}

func TestWizardLoadsBackupAndSelectsServer(t *testing.T) {
	path := writeBackup(t, globals(srtpServer("Home"), srtpServer("Office")))
	var output bytes.Buffer
	o := wizardOptions(t, "-block-ipv6=false")
	profile, err := runSetupWizard(strings.NewReader("1\n"+path+"\n2\n\n\n"), &output, o)
	if err != nil {
		t.Fatal(err)
	}
	server, err := profile.selectedServer(o.serverName)
	if err != nil || server.Name != "Office" {
		t.Fatal("wizard ignored the selected backup server")
	}
	if o.blockIPv6 {
		t.Fatal("wizard replaced an explicit IPv6 flag")
	}
}

func TestWizardAcceptsMultilineJSONAndBareBase64(t *testing.T) {
	raw := wizardProfile(t)
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, []byte(raw), "", "  "); err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{pretty.String() + "\n.end", base64.RawURLEncoding.EncodeToString([]byte(raw))} {
		if _, err := runSetupWizard(strings.NewReader("1\n"+input+"\n\n\n"), io.Discard, wizardOptions(t)); err != nil {
			t.Fatal(err)
		}
	}
}

func manualWizardConfig() string {
	return "[Interface]\nPrivateKey = " + testKey(1) + "\nAddress = 10.66.66.2/32\nDNS = 1.1.1.1\n[Peer]\nPublicKey = " + testKey(2) + "\nEndpoint = example.com:51820\nAllowedIPs = 0.0.0.0/0, ::/0\nPersistentKeepalive = 30"
}

func TestWizardBuildsManualWireGuardProfile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "client.conf")
	if err := os.WriteFile(path, []byte(manualWizardConfig()), 0600); err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{path, "\n" + manualWizardConfig() + "\n.end"} {
		input := "2\n" + source + "\nhttps://vk.ru/call/join/test\nexample.com:56000\n2\n\n\n"
		profile, err := runSetupWizard(strings.NewReader(input), io.Discard, wizardOptions(t))
		if err != nil {
			t.Fatal(err)
		}
		server, err := profile.selectedServer("")
		if err != nil {
			t.Fatal(err)
		}
		settings := buildSettings(profile, server)
		if settings.PeerAddress != "example.com:56000" || settings.UseSrtp || settings.PersistentKeepalive != 30 {
			t.Fatal("manual wizard changed the selected configuration")
		}
	}
}

func TestWizardRussianLanguageAndInvalidMenuRetry(t *testing.T) {
	o, err := parseFlags([]string{"-wizard"})
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	input := "wrong\n2\n1\n" + wizardProfile(t) + "\n\n\n"
	if _, err := runSetupWizard(strings.NewReader(input), &output, o); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Настройка подключения") || !strings.Contains(output.String(), "Press Enter") && !strings.Contains(output.String(), "Нажмите Enter") {
		t.Fatal("wizard did not switch to Russian")
	}
}

func TestWizardRejectsInvalidInputsWithoutEchoingThem(t *testing.T) {
	for _, input := range []string{
		"1\nsecret-that-is-not-a-profile\n",
		"1\n" + strings.Repeat("x", wizardInputLimit+1) + "\n",
		"2\n\n[Interface]\nPrivateKey=secret\n.end\nhttps://vk.ru/call/join/test\nexample.com:56000\n1\n",
	} {
		var output bytes.Buffer
		_, err := runSetupWizard(strings.NewReader(input), &output, wizardOptions(t))
		if err == nil {
			t.Fatal("wizard accepted invalid input")
		}
		if strings.Contains(err.Error(), "secret-that-is-not-a-profile") || strings.Contains(output.String(), "PrivateKey=secret") {
			t.Fatal("wizard echoed an invalid secret")
		}
	}
}

func TestWizardStopsOnEOFAndValidatesLanguageFlag(t *testing.T) {
	if _, err := runSetupWizard(strings.NewReader(""), io.Discard, wizardOptions(t)); !errors.Is(err, io.EOF) {
		t.Fatalf("unexpected EOF result: %v", err)
	}
	if _, err := parseFlags([]string{"-lang", "de"}); err == nil {
		t.Fatal("unsupported language was accepted")
	}
}

func TestWizardListDoesNotAskToConnect(t *testing.T) {
	var output bytes.Buffer
	if _, err := runSetupWizard(strings.NewReader("1\n"+wizardProfile(t)+"\n"), &output, wizardOptions(t, "-list")); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "Press Enter to connect") {
		t.Fatal("list mode asked to start a connection")
	}
}
