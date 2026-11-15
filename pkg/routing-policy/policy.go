// SPDX-License-Identifier: MIT

package routingpolicy

import (
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"strings"

	"golang.org/x/net/idna"
)

const MaxPolicyBytes = 32 << 20

// Settings are stored with a connection profile. An empty mode retains full-tunnel behavior.
type Settings struct {
	Mode        string   `json:"mode"`
	GeoIP       []string `json:"geoip"`
	GeoSite     []string `json:"geosite"`
	Custom      string   `json:"custom"`
	LocalDirect bool     `json:"localDirect"`
}

// Policy is a resolved, self-contained snapshot passed to the privileged console.
type Policy struct {
	Mode        string   `json:"mode"`
	Prefixes    []string `json:"prefixes"`
	Domains     []Domain `json:"domains"`
	LocalDirect bool     `json:"localDirect"`
}

type Domain struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

func (s Settings) Validate() error {
	if s.Mode != "" && s.Mode != "include" && s.Mode != "exclude" {
		return errors.New("Unknown routing mode.")
	}
	if len(s.Custom) > 1<<20 || len(s.GeoIP)+len(s.GeoSite) > 8192 {
		return errors.New("Routing settings exceed the supported size.")
	}
	for _, tags := range [][]string{s.GeoIP, s.GeoSite} {
		for _, tag := range tags {
			if tag == "" || len(tag) > 256 || strings.ContainsAny(tag, "\r\n\x00") {
				return errors.New("Invalid routing data tag.")
			}
		}
	}
	if s.Mode == "" {
		return nil
	}
	_, _, err := ParseCustom(s.Custom)
	return err
}

// ParseCustom accepts one IP, IPv4 subnet, or domain suffix per line or comma.
func ParseCustom(text string) ([]string, []Domain, error) {
	var prefixes []string
	var domains []Domain
	for _, item := range strings.FieldsFunc(text, func(r rune) bool { return r == '\n' || r == '\r' || r == ',' }) {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if ip, err := netip.ParseAddr(item); err == nil {
			if !ip.Is4() {
				return nil, nil, errors.New("Routing supports IPv4 addresses and subnets. IPv6 is blocked while connected.")
			}
			prefixes = append(prefixes, netip.PrefixFrom(ip, 32).String())
			continue
		}
		if prefix, err := netip.ParsePrefix(item); err == nil {
			if !prefix.Addr().Is4() {
				return nil, nil, errors.New("Routing supports IPv4 addresses and subnets. IPv6 is blocked while connected.")
			}
			prefixes = append(prefixes, prefix.Masked().String())
			continue
		}
		name, err := normalizeDomain(strings.TrimPrefix(item, "*."))
		if err != nil {
			return nil, nil, fmt.Errorf("Invalid routing entry %q. Enter a domain, IPv4 address or subnet.", item)
		}
		domains = append(domains, Domain{Type: "domain", Value: name})
	}
	return prefixes, domains, nil
}

func normalizeDomain(value string) (string, error) {
	name, err := idna.Lookup.ToASCII(strings.TrimSuffix(strings.ToLower(value), "."))
	if err != nil || name == "" || len(name) > 253 || strings.ContainsAny(name, "/:@ \\?*#\t\n\x00") {
		return "", errors.New("Invalid domain name.")
	}
	if _, err := netip.ParseAddr(name); err == nil {
		return "", errors.New("Expected a domain name.")
	}
	for _, label := range strings.Split(name, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", errors.New("Invalid domain label.")
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return "", errors.New("Invalid domain label.")
			}
		}
	}
	return name, nil
}

type Matcher struct {
	prefixes map[netip.Prefix]struct{}
	suffixes map[string]struct{}
	exact    map[string]struct{}
	plain    []string
	regex    []*regexp.Regexp
}

func (p Policy) Validate() error {
	_, err := Compile(p)
	return err
}

func Compile(p Policy) (*Matcher, error) {
	if p.Mode != "include" && p.Mode != "exclude" {
		return nil, errors.New("Unknown routing policy mode.")
	}
	if len(p.Prefixes)+len(p.Domains) > 1000000 {
		return nil, errors.New("Routing policy exceeds the supported number of rules.")
	}
	m := &Matcher{prefixes: make(map[netip.Prefix]struct{}, len(p.Prefixes)), suffixes: map[string]struct{}{}, exact: map[string]struct{}{}}
	for _, value := range p.Prefixes {
		prefix, err := netip.ParsePrefix(value)
		if err != nil || !prefix.Addr().Is4() {
			return nil, errors.New("Routing policies require IPv4 subnets.")
		}
		m.prefixes[prefix.Masked()] = struct{}{}
	}
	for _, domain := range p.Domains {
		if domain.Value == "" || len(domain.Value) > 4096 {
			return nil, errors.New("Invalid routing domain rule.")
		}
		switch domain.Type {
		case "domain", "full":
			name, err := normalizeDomain(domain.Value)
			if err != nil {
				return nil, fmt.Errorf("Invalid routing domain %q.", domain.Value)
			}
			if domain.Type == "domain" {
				m.suffixes[name] = struct{}{}
			} else {
				m.exact[name] = struct{}{}
			}
		case "plain":
			m.plain = append(m.plain, strings.ToLower(domain.Value))
		case "regex":
			pattern, err := regexp.Compile("(?i)" + domain.Value)
			if err != nil {
				return nil, fmt.Errorf("Invalid routing regular expression: %w", err)
			}
			m.regex = append(m.regex, pattern)
		default:
			return nil, errors.New("Unknown routing domain rule type.")
		}
	}
	return m, nil
}

func (m *Matcher) MatchesIP(addr netip.Addr) bool {
	if !addr.Is4() {
		return false
	}
	for bits := 32; bits >= 0; bits-- {
		if _, ok := m.prefixes[netip.PrefixFrom(addr, bits).Masked()]; ok {
			return true
		}
	}
	return false
}

func (m *Matcher) MatchesDomain(value string) bool {
	name := strings.TrimSuffix(strings.ToLower(value), ".")
	if _, ok := m.exact[name]; ok {
		return true
	}
	for suffix := name; suffix != ""; {
		if _, ok := m.suffixes[suffix]; ok {
			return true
		}
		_, suffix, _ = strings.Cut(suffix, ".")
	}
	for _, plain := range m.plain {
		if strings.Contains(name, plain) {
			return true
		}
	}
	for _, pattern := range m.regex {
		if pattern.MatchString(name) {
			return true
		}
	}
	return false
}
