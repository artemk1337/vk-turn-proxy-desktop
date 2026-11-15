// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"log"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

type policyDNS struct {
	udp      net.PacketConn
	tcp      net.Listener
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	slots    chan struct{}
	exchange func(context.Context, string, []byte) ([]byte, error)
	route    func(string, netip.Addr) error
}

func startPolicyDNS(parent context.Context, address string, exchange func(context.Context, string, []byte) ([]byte, error), route func(string, netip.Addr) error) (*policyDNS, error) {
	udp, err := net.ListenPacket("udp4", address)
	if err != nil {
		return nil, err
	}
	// An ephemeral test listener uses the same port for UDP and TCP.
	tcp, err := net.Listen("tcp4", udp.LocalAddr().String())
	if err != nil {
		udp.Close()
		return nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	d := &policyDNS{udp: udp, tcp: tcp, cancel: cancel, slots: make(chan struct{}, 64), exchange: exchange, route: route}
	d.wg.Add(2)
	go d.serveUDP(ctx)
	go d.serveTCP(ctx)
	return d, nil
}

func (d *policyDNS) close() {
	d.cancel()
	d.udp.Close()
	d.tcp.Close()
	d.wg.Wait()
}

func (d *policyDNS) serveUDP(ctx context.Context) {
	defer d.wg.Done()
	buffer := make([]byte, 65535)
	for {
		n, peer, err := d.udp.ReadFrom(buffer)
		if err != nil {
			return
		}
		select {
		case d.slots <- struct{}{}:
		case <-ctx.Done():
			return
		default:
			continue
		}
		query := bytes.Clone(buffer[:n])
		d.wg.Add(1)
		go func() {
			defer d.wg.Done()
			defer func() { <-d.slots }()
			answer := d.answer(ctx, "udp", query)
			if len(answer) > 0 {
				_, _ = d.udp.WriteTo(answer, peer)
			}
		}()
	}
}

func (d *policyDNS) serveTCP(ctx context.Context) {
	defer d.wg.Done()
	for {
		conn, err := d.tcp.Accept()
		if err != nil {
			return
		}
		select {
		case d.slots <- struct{}{}:
		case <-ctx.Done():
			conn.Close()
			return
		default:
			conn.Close()
			continue
		}
		d.wg.Add(1)
		go func() {
			defer d.wg.Done()
			defer func() { <-d.slots }()
			defer conn.Close()
			for ctx.Err() == nil {
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				query, err := readDNSFrame(conn)
				if err != nil {
					return
				}
				answer := d.answer(ctx, "tcp", query)
				if len(answer) == 0 || writeDNSFrame(conn, answer) != nil {
					return
				}
			}
		}()
	}
}

func dnsError(query dnsmessage.Message, code dnsmessage.RCode) []byte {
	header := dnsmessage.Header{ID: query.ID, Response: true, RecursionDesired: query.RecursionDesired, RecursionAvailable: true, RCode: code}
	answer, _ := (&dnsmessage.Message{Header: header, Questions: query.Questions}).Pack()
	return answer
}

func (d *policyDNS) answer(parent context.Context, network string, data []byte) []byte {
	var query dnsmessage.Message
	if query.Unpack(data) != nil || query.Response || len(query.Questions) != 1 || query.OpCode != 0 {
		return nil
	}
	if query.Questions[0].Type == dnsmessage.TypeAAAA {
		return dnsError(query, dnsmessage.RCodeSuccess) // the policy tunnel is IPv4 only
	}
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	raw, err := d.exchange(ctx, network, data)
	if err != nil {
		return dnsError(query, dnsmessage.RCodeServerFailure)
	}
	var answer dnsmessage.Message
	if answer.Unpack(raw) != nil || !answer.Response || answer.ID != query.ID || len(answer.Questions) != 1 || !strings.EqualFold(answer.Questions[0].Name.String(), query.Questions[0].Name.String()) || answer.Questions[0].Type != query.Questions[0].Type {
		return dnsError(query, dnsmessage.RCodeServerFailure)
	}
	resources := append(append([]dnsmessage.Resource{}, answer.Answers...), answer.Additionals...)
	if err := d.routeAnswer(query.Questions[0].Name.String(), resources); err != nil {
		log.Printf("routing: DNS destination route failed: %v", err)
		return dnsError(query, dnsmessage.RCodeServerFailure)
	}
	// Some clients ask ANY. Never return AAAA records that could bypass the
	// IPv4 policy, including records in additional sections.
	changed := false
	for _, section := range []*[]dnsmessage.Resource{&answer.Answers, &answer.Authorities, &answer.Additionals} {
		kept := (*section)[:0]
		for _, resource := range *section {
			if resource.Header.Type == dnsmessage.TypeAAAA {
				changed = true
				continue
			}
			kept = append(kept, resource)
		}
		*section = kept
	}
	if changed {
		answer.AuthenticData = false
		raw, err = answer.Pack()
		if err != nil {
			return dnsError(query, dnsmessage.RCodeServerFailure)
		}
	}
	return raw
}

func (d *policyDNS) routeAnswer(question string, answers []dnsmessage.Resource) error {
	// Follow only the requested CNAME chain. Unrelated glue must not create
	// routes, while an alias into a selected GeoSite domain must still match.
	type record struct {
		owner, target string
		addresses     []netip.Addr
	}
	records := make([]record, 0, len(answers))
	for _, resource := range answers {
		item := record{owner: strings.ToLower(resource.Header.Name.String())}
		switch body := resource.Body.(type) {
		case *dnsmessage.AResource:
			item.addresses = []netip.Addr{netip.AddrFrom4(body.A)}
		case *dnsmessage.CNAMEResource:
			item.target = strings.ToLower(body.CNAME.String())
		default:
			var err error
			item.target, item.addresses, err = policySVCB(resource)
			if err != nil {
				return err
			}
			if item.target == "." {
				item.target = item.owner
			}
		}
		records = append(records, item)
	}
	name := strings.ToLower(question)
	paths := map[string][]string{name: {name}}
	for range len(records) {
		added := false
		for _, item := range records {
			path, reachable := paths[item.owner]
			if !reachable || item.target == "" {
				continue
			}
			if _, exists := paths[item.target]; !exists {
				paths[item.target] = append(append([]string{}, path...), item.target)
				added = true
			}
		}
		if !added {
			break
		}
	}
	for _, item := range records {
		path, reachable := paths[item.owner]
		if !reachable {
			continue
		}
		if item.target != "" && item.target != item.owner {
			path = append(append([]string{}, path...), item.target)
		}
		for _, addr := range item.addresses {
			for _, name := range path {
				if err := d.route(name, addr); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func readDNSFrame(reader io.Reader) ([]byte, error) {
	var size [2]byte
	if _, err := io.ReadFull(reader, size[:]); err != nil {
		return nil, err
	}
	length := binary.BigEndian.Uint16(size[:])
	if length < 12 {
		return nil, errors.New("invalid DNS frame")
	}
	message := make([]byte, int(length))
	_, err := io.ReadFull(reader, message)
	return message, err
}

func writeDNSFrame(writer io.Writer, message []byte) error {
	if len(message) < 12 || len(message) > 65535 {
		return errors.New("invalid DNS message size")
	}
	frame := make([]byte, len(message)+2)
	binary.BigEndian.PutUint16(frame, uint16(len(message)))
	copy(frame[2:], message)
	_, err := io.Copy(writer, bytes.NewReader(frame))
	return err
}

func exchangePolicyDNS(ctx context.Context, network, address string, query []byte) ([]byte, error) {
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, network, address)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	deadline := time.Now().Add(3 * time.Second)
	if end, ok := ctx.Deadline(); ok && end.Before(deadline) {
		deadline = end
	}
	_ = conn.SetDeadline(deadline)
	if network == "tcp" {
		if err = writeDNSFrame(conn, query); err != nil {
			return nil, err
		}
		return readDNSFrame(conn)
	}
	if _, err = conn.Write(query); err != nil {
		return nil, err
	}
	buffer := make([]byte, 65535)
	n, err := conn.Read(buffer)
	return buffer[:n], err
}
