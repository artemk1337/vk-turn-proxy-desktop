// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/netip"
	"os"
	"sync"

	routingpolicy "github.com/artemk1337/vk-turn-proxy-desktop/pkg/routing-policy"
)

type policyRuntime struct {
	policy      routingpolicy.Policy
	matcher     *routingpolicy.Matcher
	dns         *policyDNS
	mu          sync.Mutex
	closed      bool
	routed      map[netip.Addr]bool
	staticHosts map[netip.Addr]bool
	tunnelHosts map[netip.Addr]string
	resolvers   map[netip.Addr]bool
}

func decodeRoutingPolicy(data []byte) (routingpolicy.Policy, error) {
	var policy routingpolicy.Policy
	if len(data) == 0 || len(data) > routingpolicy.MaxPolicyBytes {
		return policy, errors.New("invalid routing policy size")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&policy); err != nil {
		return policy, fmt.Errorf("invalid routing policy: %w", err)
	}
	if decoder.Decode(new(any)) != io.EOF {
		return policy, errors.New("unexpected trailing routing policy data")
	}
	return policy, policy.Validate()
}

func readRoutingPolicy(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, routingpolicy.MaxPolicyBytes+1))
	if err != nil {
		return nil, err
	}
	if _, err = decodeRoutingPolicy(data); err != nil {
		return nil, err
	}
	return data, nil
}

func loadPolicyRuntime(path string) (*policyRuntime, error) {
	if path == "" {
		return nil, nil
	}
	data, err := readRoutingPolicy(path)
	if err != nil {
		return nil, err
	}
	policy, err := decodeRoutingPolicy(data)
	if err != nil {
		return nil, err
	}
	matcher, err := routingpolicy.Compile(policy)
	if err != nil {
		return nil, err
	}
	return &policyRuntime{policy: policy, matcher: matcher, routed: make(map[netip.Addr]bool), staticHosts: make(map[netip.Addr]bool), tunnelHosts: make(map[netip.Addr]string), resolvers: make(map[netip.Addr]bool)}, nil
}

func (c *console) activatePolicy(ctx context.Context) error {
	policy := c.routing
	prefixes, err := routingpolicy.TunnelPrefixes(policy.policy)
	if err != nil {
		return err
	}
	// Bind both transports before changing routes or the system resolver.
	dns, err := startPolicyDNS(ctx, "127.0.0.1:53", c.policyDNSExchange, c.routeDNSAnswer)
	if err != nil {
		return fmt.Errorf("cannot listen on 127.0.0.1:53: %w", err)
	}
	policy.dns = dns
	if err := c.installPolicyRoutes(ctx, prefixes); err != nil {
		return err
	}
	for _, server := range c.dnsPlan.servers {
		if err := ctx.Err(); err != nil {
			return err
		}
		if addr, err := netip.ParseAddr(server); err == nil && addr.Is4() && !addr.IsLoopback() {
			if err := c.ensurePolicyResolver(addr); err != nil {
				return fmt.Errorf("routing profile DNS: %w", err)
			}
		}
	}
	for _, route := range hostCmds.ipv6Block(c.tun) {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := c.j.add(undoStep{Key: "route ipv6 " + route.key, Argv: route.del}); err != nil {
			return err
		}
		if _, err := runCmd(route.add); err != nil {
			return fmt.Errorf("blocking IPv6: %w", err)
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := c.applySystemDNS(); err != nil {
		return fmt.Errorf("applying policy DNS: %w", err)
	}
	log.Printf("routing: ready, %s mode, %d IPv4 routes, %d domain rules; DNS listening on 127.0.0.1:53", policy.policy.Mode, len(prefixes), len(policy.policy.Domains))
	return nil
}

func (c *console) installPolicyRoutes(ctx context.Context, prefixes []netip.Prefix) error {
	policy := c.routing
	policy.mu.Lock()
	defer policy.mu.Unlock()
	batch := policyRouteBatch{Tunnel: c.tun, Prefixes: make([]string, 0, len(prefixes))}
	for _, prefix := range prefixes {
		if prefix.Bits() == 32 {
			c.pin.mu.Lock()
			protected := c.pin.wanted[prefix.Addr().String()]
			c.pin.mu.Unlock()
			if protected {
				continue
			}
			policy.staticHosts[prefix.Addr()] = true
		}
		batch.Prefixes = append(batch.Prefixes, prefix.String())
	}
	if len(batch.Prefixes) > 0 {
		if err := c.j.add(undoStep{Key: "route policy", PolicyRoutes: &batch}); err != nil {
			return err
		}
		run := func(argv []string) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			_, err := runCmd(argv)
			return err
		}
		if err := runPolicyBatch(batch, true, hostCmds, run); err != nil {
			return fmt.Errorf("applying IPv4 route plan: %w", err)
		}
	}
	return nil
}

func (p *policyRuntime) close() {
	p.mu.Lock()
	p.closed = true
	p.mu.Unlock()
	if p.dns != nil {
		p.dns.close()
	}
}

// A selected DNS destination receives its route before the answer is visible
// to the application. Physical pins and local-network exclusions take priority.
func (c *console) routeDNSAnswer(name string, addr netip.Addr) error {
	p := c.routing
	if !addr.Is4() || p.policy.LocalDirect && routingpolicy.IsLocal(addr) {
		return nil
	}
	if !p.matcher.MatchesDomain(name) || p.matcher.MatchesIP(addr) {
		return nil // the static CIDR plan already carries selected addresses
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return errors.New("routing is stopping")
	}
	if p.resolvers[addr] {
		return nil
	}
	if p.routed[addr] {
		if p.policy.Mode == "exclude" {
			return c.pin.ensure(addr.String())
		}
		return nil
	}
	if len(p.routed) >= 8192 {
		return errors.New("the DNS routing table is full; reconnect to clear learned routes")
	}
	if p.policy.Mode == "exclude" {
		if err := c.pin.ensure(addr.String()); err != nil {
			return err
		}
		p.routed[addr] = true
		return nil
	}
	c.pin.mu.Lock()
	protected := c.pin.wanted[addr.String()]
	c.pin.mu.Unlock()
	if protected {
		return nil
	}
	route := policyRoute(hostCmds, c.tun, netip.PrefixFrom(addr, 32).String())
	key := "route domain " + addr.String()
	if err := c.j.add(undoStep{Key: key, Argv: route.del}); err != nil {
		return err
	}
	output, err := runCmd(route.add)
	if err != nil {
		if routeAlreadyExists(output, err) {
			_ = c.j.done(key)
			return errors.New("a destination route already exists")
		}
		return err
	}
	p.routed[addr] = true
	p.tunnelHosts[addr] = key
	return nil
}

func routeAlreadyExists(output string, err error) bool {
	return err != nil && bytes.Contains([]byte(output+err.Error()), []byte("File exists"))
}

func (c *console) policyDNSExchange(ctx context.Context, network string, query []byte) ([]byte, error) {
	var last error = errors.New("no usable upstream DNS server")
	for _, server := range c.policyUpstreams() {
		addr, err := netip.ParseAddr(server.address)
		if err != nil || !addr.Is4() || addr.IsLoopback() || addr.IsUnspecified() {
			continue // forwarding to our own resolver would recurse
		}
		if server.tunnel {
			err = c.ensurePolicyResolver(addr)
		} else {
			err = c.ensurePhysicalIP(server.address)
		}
		if err != nil {
			last = err
			continue
		}
		answer, err := exchangePolicyDNS(ctx, network, net.JoinHostPort(server.address, "53"), query)
		if err == nil {
			return answer, nil
		}
		last = err
	}
	return nil, last
}

type policyUpstream struct {
	address string
	tunnel  bool
}

func (c *console) policyUpstreams() []policyUpstream {
	var servers []policyUpstream
	seen := map[string]bool{}
	for _, source := range []struct {
		addresses []string
		tunnel    bool
	}{{c.dnsPlan.servers, true}, {c.src.list(), false}} {
		for _, address := range source.addresses {
			if !seen[address] {
				servers = append(servers, policyUpstream{address, source.tunnel})
				seen[address] = true
			}
		}
	}
	return servers
}

// Profile resolvers stay reachable through the tunnel even when their
// private addresses are outside the WireGuard interface's own subnet.
func (c *console) ensurePolicyResolver(addr netip.Addr) error {
	p := c.routing
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return errors.New("routing is stopping")
	}
	if p.resolvers[addr] {
		return nil
	}
	c.pin.mu.Lock()
	protected := c.pin.wanted[addr.String()]
	c.pin.mu.Unlock()
	if protected {
		return c.pin.ensure(addr.String()) // transport recovery keeps priority at a shared DNS address
	}
	if p.staticHosts[addr] || p.tunnelHosts[addr] != "" {
		p.resolvers[addr] = true
		return nil
	}
	route := policyRoute(hostCmds, c.tun, netip.PrefixFrom(addr, 32).String())
	key := "route resolver " + addr.String()
	if err := c.j.add(undoStep{Key: key, Argv: route.del}); err != nil {
		return err
	}
	output, err := runCmd(route.add)
	if err != nil {
		if routeAlreadyExists(output, err) {
			_ = c.j.done(key)
		}
		return err
	}
	p.tunnelHosts[addr] = key
	p.resolvers[addr] = true
	return nil
}

func (c *console) policyDialHook(network, address string) error {
	host, _, err := net.SplitHostPort(address)
	if err == nil {
		if addr, err := netip.ParseAddr(host); err == nil && addr.Unmap().Is4() {
			return c.ensurePhysicalIP(addr.Unmap().String())
		}
	}
	return c.pin.hook(network, address)
}

// A DNS address may later become a TURN, VK or resolver destination. Release
// our exact tunnel route before the pinner installs its physical-path /32.
func (c *console) ensurePhysicalIP(ip string) error {
	p := c.routing
	if p == nil {
		return c.pin.ensure(ip)
	}
	addr, err := netip.ParseAddr(ip)
	if err != nil || !addr.Is4() {
		return c.pin.ensure(ip)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return errors.New("routing is stopping")
	}
	key := p.tunnelHosts[addr]
	if p.staticHosts[addr] || key != "" {
		route := policyRoute(hostCmds, c.tun, netip.PrefixFrom(addr, 32).String())
		if _, err := runCmd(route.del); err != nil && !alreadyGone(err) {
			return err
		}
		if key != "" {
			if err := c.j.done(key); err != nil {
				return err
			}
			delete(p.routed, addr)
			delete(p.tunnelHosts, addr)
		}
		delete(p.staticHosts, addr)
		delete(p.resolvers, addr)
	}
	return c.pin.ensure(ip)
}
