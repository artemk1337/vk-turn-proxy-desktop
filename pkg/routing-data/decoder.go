// SPDX-License-Identifier: MIT
package routingdata

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"strings"
	"unicode/utf8"
)

type Domain struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

type Rules struct {
	Prefixes []string `json:"prefixes"`
	Domains  []Domain `json:"domains"`
}

type field struct {
	number int
	wire   uint64
	value  uint64
	bytes  []byte
}

// fields reads the protobuf wire format used by V2Ray GeoIPList and GeoSiteList.
// Unknown scalar fields remain forward compatible; groups are unsupported.
func fields(data []byte, visit func(field) error) error {
	for len(data) > 0 {
		key, n := binary.Uvarint(data)
		if n <= 0 || key>>3 == 0 || key>>3 > 536870911 {
			return errors.New("invalid protobuf field")
		}
		data = data[n:]
		f := field{number: int(key >> 3), wire: key & 7}
		switch key & 7 {
		case 0:
			value, size := binary.Uvarint(data)
			if size <= 0 {
				return errors.New("invalid protobuf integer")
			}
			f.value = value
			data = data[size:]
		case 1:
			if len(data) < 8 {
				return errors.New("truncated protobuf fixed64")
			}
			data = data[8:]
		case 2:
			size, count := binary.Uvarint(data)
			if count <= 0 || size > uint64(len(data)-count) {
				return errors.New("truncated protobuf message")
			}
			data = data[count:]
			f.bytes = data[:int(size)]
			data = data[int(size):]
		case 5:
			if len(data) < 4 {
				return errors.New("truncated protobuf fixed32")
			}
			data = data[4:]
		default:
			return errors.New("unsupported protobuf wire type")
		}
		if err := visit(f); err != nil {
			return err
		}
	}
	return nil
}

type dataset struct {
	ip      map[string][]string
	sites   map[string][]Domain
	inverse map[string]bool
}

func decode(ipData, siteData []byte) (dataset, error) {
	result := dataset{ip: make(map[string][]string), sites: make(map[string][]Domain), inverse: make(map[string]bool)}
	memberCount := 0
	for _, source := range []struct {
		data []byte
		ip   bool
	}{{ipData, true}, {siteData, false}} {
		err := fields(source.data, func(f field) error {
			if f.number != 1 {
				return nil
			}
			if f.wire != 2 {
				return errors.New("invalid routing list wire type")
			}
			if len(result.ip)+len(result.sites) >= 100000 {
				return errors.New("routing tag count exceeds limit")
			}
			var tag string
			var members [][]byte
			var inverse bool
			if err := fields(f.bytes, func(entry field) error {
				if (entry.number == 1 || entry.number == 2) && entry.wire != 2 {
					return errors.New("invalid routing entry wire type")
				}
				if source.ip && entry.number == 3 && entry.wire != 0 {
					return errors.New("invalid GeoIP inverse wire type")
				}
				switch entry.number {
				case 1:
					tag = strings.ToLower(string(entry.bytes))
				case 2:
					memberCount++
					if memberCount > 4000000 {
						return errors.New("routing rule count exceeds limit")
					}
					members = append(members, entry.bytes)
				case 3:
					inverse = entry.value != 0
				}
				return nil
			}); err != nil {
				return err
			}
			if tag == "" || len(tag) > 200 || !utf8.ValidString(tag) {
				return errors.New("invalid routing tag")
			}
			if source.ip {
				if _, exists := result.ip[tag]; exists {
					return fmt.Errorf("duplicate GeoIP tag %s", tag)
				}
				result.ip[tag] = nil
				result.inverse[tag] = inverse
				for _, member := range members {
					var address []byte
					var prefix uint64
					if err := fields(member, func(cidr field) error {
						if cidr.number == 1 && cidr.wire != 2 || cidr.number == 2 && cidr.wire != 0 {
							return errors.New("invalid GeoIP CIDR wire type")
						}
						if cidr.number == 1 {
							address = cidr.bytes
						}
						if cidr.number == 2 {
							prefix = cidr.value
						}
						return nil
					}); err != nil {
						return err
					}
					addr, ok := netip.AddrFromSlice(address)
					if !ok || prefix > uint64(addr.BitLen()) {
						return errors.New("invalid GeoIP prefix")
					}
					result.ip[tag] = append(result.ip[tag], netip.PrefixFrom(addr, int(prefix)).Masked().String())
				}
				return nil
			}
			if _, exists := result.sites[tag]; exists {
				return fmt.Errorf("duplicate GeoSite tag %s", tag)
			}
			result.sites[tag] = nil
			for _, member := range members {
				var kind uint64
				var value string
				if err := fields(member, func(domain field) error {
					if domain.number == 1 && domain.wire != 0 || domain.number == 2 && domain.wire != 2 {
						return errors.New("invalid GeoSite domain wire type")
					}
					if domain.number == 1 {
						kind = domain.value
					}
					if domain.number == 2 {
						value = string(domain.bytes)
					}
					return nil
				}); err != nil {
					return err
				}
				if kind > 3 || value == "" || len(value) > 4096 || !utf8.ValidString(value) {
					return errors.New("invalid GeoSite domain")
				}
				if kind == 1 {
					if _, err := regexp.Compile(value); err != nil {
						return fmt.Errorf("GeoSite %s: %w", tag, err)
					}
				}
				result.sites[tag] = append(result.sites[tag], Domain{Type: []string{"plain", "regex", "domain", "full"}[kind], Value: value})
			}
			return nil
		})
		if err != nil {
			return dataset{}, err
		}
	}
	if len(result.ip) == 0 || len(result.sites) == 0 {
		return dataset{}, errors.New("empty routing dataset")
	}
	return result, nil
}
