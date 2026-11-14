// SPDX-License-Identifier: MIT

// Package wgprofile converts a full-tunnel WireGuard configuration into an iOS
// connection profile understood by the VK TURN desktop client.
package wgprofile

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"unicode"
)

type Options struct {
	Config      string
	VKLink      string
	PeerAddress string
	UseSrtp     bool
}

// Build preserves WireGuard keys and produces the connection format used by
// anton48/vk-turn-proxy-ios. PeerAddress overrides the configuration's Endpoint.
func Build(o Options) ([]byte, error) {
	values, err := parseConfig(o.Config)
	if err != nil {
		return nil, err
	}
	for _, field := range []struct{ key, label string }{{"interface.privatekey", "PrivateKey"}, {"peer.publickey", "PublicKey"}} {
		if err := validateKey(values[field.key]); err != nil {
			return nil, fmt.Errorf("%s must be a base64-encoded 32-byte WireGuard key", field.label)
		}
	}
	if key := values["peer.presharedkey"]; key != "" {
		if err := validateKey(key); err != nil {
			return nil, fmt.Errorf("PresharedKey must be a base64-encoded 32-byte WireGuard key")
		}
	}
	address, err := tunnelAddress(values["interface.address"])
	if err != nil {
		return nil, err
	}
	if err := allowedIPs(values["peer.allowedips"]); err != nil {
		return nil, err
	}
	dns := splitValues(values["interface.dns"])
	for _, value := range dns {
		ip, err := netip.ParseAddr(value)
		if err != nil || !ip.Is4() {
			return nil, fmt.Errorf("DNS must contain IPv4 addresses")
		}
	}
	endpoint := strings.TrimSpace(o.PeerAddress)
	if endpoint == "" {
		endpoint = values["peer.endpoint"]
	}
	if err := validateEndpoint(endpoint); err != nil {
		return nil, err
	}
	vkLink, err := callLink(o.VKLink)
	if err != nil {
		return nil, err
	}
	settings := map[string]any{
		"serverName":          "WireGuard profile",
		"privateKey":          values["interface.privatekey"],
		"peerPublicKey":       values["peer.publickey"],
		"tunnelAddress":       address,
		"peerAddress":         endpoint,
		"vkLink":              vkLink,
		"useSrtp":             o.UseSrtp,
		"useDTLS":             true,
		"useUDP":              false,
		"persistentKeepalive": 0,
	}
	if value := values["peer.presharedkey"]; value != "" {
		settings["presharedKey"] = value
	}
	if len(dns) > 0 {
		settings["dnsServers"] = strings.Join(dns, ", ")
	}
	if value, ok := values["interface.mtu"]; ok {
		mtu, err := strconv.Atoi(value)
		if err != nil || mtu < 1000 || mtu > 1400 {
			return nil, fmt.Errorf("MTU must be between 1000 and 1400 for the VK TURN tunnel")
		}
		settings["tunnelMTU"] = mtu
	}
	if value, ok := values["peer.persistentkeepalive"]; ok {
		if strings.EqualFold(value, "off") {
			value = "0"
		}
		keepalive, err := strconv.Atoi(value)
		if err != nil || keepalive < 0 || keepalive > 65535 {
			return nil, fmt.Errorf("PersistentKeepalive must be between 0 and 65535 seconds")
		}
		settings["persistentKeepalive"] = keepalive
	}
	return json.Marshal(struct {
		Version  int            `json:"version"`
		Type     string         `json:"type"`
		Settings map[string]any `json:"settings"`
	}{Version: 1, Type: "connection", Settings: settings})
}

func parseConfig(config string) (map[string]string, error) {
	if len(config) > 4<<20 {
		return nil, fmt.Errorf("WireGuard configuration exceeds 4 MiB")
	}
	values := make(map[string]string)
	sections := make(map[string]bool)
	section := ""
	config = strings.TrimPrefix(strings.TrimSpace(config), "\ufeff")
	for index, line := range strings.Split(config, "\n") {
		line = strings.TrimSpace(strings.SplitN(strings.SplitN(line, "#", 2)[0], ";", 2)[0])
		if line == "" {
			continue
		}
		if strings.IndexFunc(line, func(r rune) bool { return unicode.IsControl(r) && r != '\t' }) >= 0 {
			return nil, fmt.Errorf("WireGuard line %d contains control characters", index+1)
		}
		if strings.HasPrefix(line, "[") {
			section = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line, "["), "]")))
			if !strings.HasSuffix(line, "]") || (section != "interface" && section != "peer") {
				return nil, fmt.Errorf("WireGuard line %d has an unsupported section", index+1)
			}
			if sections[section] {
				return nil, fmt.Errorf("Use one Interface section and one Peer section")
			}
			sections[section] = true
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || section == "" {
			return nil, fmt.Errorf("WireGuard line %d must be a setting inside Interface or Peer", index+1)
		}
		key = section + "." + strings.ToLower(strings.TrimSpace(key))
		switch key {
		case "interface.privatekey", "interface.address", "interface.dns", "interface.mtu",
			"peer.publickey", "peer.presharedkey", "peer.endpoint", "peer.allowedips", "peer.persistentkeepalive":
		default:
			return nil, fmt.Errorf("WireGuard line %d has an unsupported setting", index+1)
		}
		if _, exists := values[key]; exists {
			return nil, fmt.Errorf("WireGuard line %d repeats a setting", index+1)
		}
		values[key] = strings.TrimSpace(value)
	}
	return values, nil
}

func validateKey(value string) error {
	key, err := base64.StdEncoding.DecodeString(value)
	if err != nil || len(key) != 32 {
		return fmt.Errorf("invalid WireGuard key")
	}
	return nil
}

func splitValues(value string) []string {
	return strings.FieldsFunc(value, func(r rune) bool { return r == ',' || unicode.IsSpace(r) })
}

func tunnelAddress(value string) (string, error) {
	firstIPv4 := ""
	for _, part := range splitValues(value) {
		prefix, err := netip.ParsePrefix(part)
		if err != nil {
			return "", fmt.Errorf("Address must contain IP addresses with CIDR prefixes")
		}
		if prefix.Addr().Is4() && firstIPv4 == "" {
			firstIPv4 = prefix.String()
		}
	}
	if firstIPv4 == "" {
		return "", fmt.Errorf("Address must contain an IPv4 address with a CIDR prefix")
	}
	return firstIPv4, nil
}

func allowedIPs(value string) error {
	if value == "" {
		return nil
	}
	fullIPv4 := false
	for _, part := range splitValues(value) {
		switch part {
		case "0.0.0.0/0":
			fullIPv4 = true
		case "::/0":
		default:
			return fmt.Errorf("Use full-tunnel AllowedIPs = 0.0.0.0/0, ::/0; individual routes are unsupported")
		}
	}
	if !fullIPv4 {
		return fmt.Errorf("AllowedIPs must include 0.0.0.0/0 for the IPv4 tunnel")
	}
	return nil
}

func validateEndpoint(value string) error {
	host, port, err := net.SplitHostPort(value)
	number, portErr := strconv.Atoi(port)
	if err != nil || host == "" || portErr != nil || number < 1 || number > 65535 || strings.IndexFunc(host, unicode.IsSpace) >= 0 || strings.IndexFunc(host, unicode.IsControl) >= 0 || strings.ContainsAny(host, "/\\?#@") || (strings.Contains(host, ":") && net.ParseIP(host) == nil) {
		return fmt.Errorf("VK TURN server must be host:port, using the proxy listener port")
	}
	return nil
}

func callLink(value string) (string, error) {
	value = strings.TrimSpace(value)
	if !strings.Contains(value, "://") {
		value = "https://" + value
	}
	u, err := url.Parse(value)
	if err != nil || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Port() != "" || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return "", fmt.Errorf("Enter a VK call invitation link")
	}
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "m.")
	path := strings.Split(strings.Trim(u.Path, "/"), "/")
	token := ""
	if (host == "vk.com" || host == "vk.ru") && len(path) == 3 && path[0] == "call" && path[1] == "join" {
		token = path[2]
	}
	if host == "vk.me" && len(path) == 2 && path[0] == "join" {
		token = path[1]
	}
	if token == "" || strings.IndexFunc(token, unicode.IsSpace) >= 0 || strings.IndexFunc(token, unicode.IsControl) >= 0 {
		return "", fmt.Errorf("Enter a VK call invitation link")
	}
	return "https://vk.ru/call/join/" + url.PathEscape(token), nil
}
