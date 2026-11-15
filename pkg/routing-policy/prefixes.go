// SPDX-License-Identifier: MIT

package routingpolicy

import (
	"encoding/binary"
	"fmt"
	"math/bits"
	"net/netip"
	"slices"
)

const ipv4Space = uint64(1) << 32

var localPrefixes = []netip.Prefix{
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("255.255.255.255/32"),
}

// IsLocal reports the IPv4 destinations kept direct by LocalDirect.
func IsLocal(addr netip.Addr) bool {
	addr = addr.Unmap()
	for _, prefix := range localPrefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

type ipRange struct {
	start uint64
	end   uint64
}

// TunnelPrefixes returns an aggregated IPv4 route set. A default route is
// represented by two /1 routes so the physical default remains available.
func TunnelPrefixes(policy Policy) ([]netip.Prefix, error) {
	if policy.Mode != "include" && policy.Mode != "exclude" {
		return nil, fmt.Errorf("unknown routing mode %q", policy.Mode)
	}
	ranges := make([]ipRange, 0, len(policy.Prefixes))
	for _, raw := range policy.Prefixes {
		prefix, err := netip.ParsePrefix(raw)
		if err != nil || !prefix.Addr().Is4() {
			return nil, fmt.Errorf("invalid IPv4 routing prefix %q", raw)
		}
		ranges = append(ranges, prefixRange(prefix))
	}
	ranges = mergeRanges(ranges)
	if policy.Mode == "exclude" {
		ranges = subtractRanges([]ipRange{{0, ipv4Space}}, ranges)
	}
	if policy.LocalDirect {
		locals := make([]ipRange, 0, len(localPrefixes))
		for _, prefix := range localPrefixes {
			locals = append(locals, prefixRange(prefix))
		}
		ranges = subtractRanges(ranges, locals)
	}
	return rangesToPrefixes(ranges), nil
}

func prefixRange(prefix netip.Prefix) ipRange {
	addr := prefix.Masked().Addr().As4()
	start := uint64(binary.BigEndian.Uint32(addr[:]))
	return ipRange{start, start + (uint64(1) << (32 - prefix.Bits()))}
}

func mergeRanges(ranges []ipRange) []ipRange {
	slices.SortFunc(ranges, func(a, b ipRange) int {
		if a.start < b.start {
			return -1
		}
		if a.start > b.start {
			return 1
		}
		return 0
	})
	merged := ranges[:0]
	for _, current := range ranges {
		if len(merged) == 0 || current.start > merged[len(merged)-1].end {
			merged = append(merged, current)
			continue
		}
		last := &merged[len(merged)-1]
		last.end = max(last.end, current.end)
	}
	return merged
}

// Both inputs are sorted, disjoint ranges with exclusive upper bounds.
func subtractRanges(ranges, exclusions []ipRange) []ipRange {
	var result []ipRange
	index := 0
	for _, current := range ranges {
		start := current.start
		for index < len(exclusions) && exclusions[index].end <= start {
			index++
		}
		for i := index; i < len(exclusions) && exclusions[i].start < current.end; i++ {
			excluded := exclusions[i]
			if excluded.start > start {
				result = append(result, ipRange{start, excluded.start})
			}
			start = max(start, excluded.end)
			if start >= current.end {
				break
			}
		}
		if start < current.end {
			result = append(result, ipRange{start, current.end})
		}
	}
	return result
}

func rangesToPrefixes(ranges []ipRange) []netip.Prefix {
	var result []netip.Prefix
	for _, current := range ranges {
		for start := current.start; start < current.end; {
			// Cap blocks at /1 to preserve the system's physical default route.
			power := min(bits.TrailingZeros64(start), bits.Len64(current.end-start)-1, 31)
			var addr [4]byte
			binary.BigEndian.PutUint32(addr[:], uint32(start))
			result = append(result, netip.PrefixFrom(netip.AddrFrom4(addr), 32-power))
			start += uint64(1) << power
		}
	}
	return result
}
