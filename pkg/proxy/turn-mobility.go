package proxy

import (
	"log"
	"net"
	"sync"
	"time"

	"github.com/pion/stun/v3"
	"github.com/pion/turn/v5"
)

const attrMobilityTicket stun.AttrType = 0x8030

var mobilityRelayReports sync.Map

// mobilityConn adds RFC 8016 negotiation to Pion's allocation requests. Pion
// retains ownership of transactions, retransmissions, and allocation refreshes.
type mobilityConn struct {
	net.PacketConn
	username, password string
	onRequest          func(*stun.Message) error
	onResponse         func(*stun.Message) error
	mu                 sync.Mutex
	forbidden          bool
	pending            map[[12]byte]mobilityRequest
}

type mobilityRequest struct {
	size    int
	to      net.Addr
	realm   string
	auth    bool
	offered bool
	method  stun.Method
	started time.Time
}

func wrapMobilityConn(conn net.PacketConn, username, password string, onRequest, onResponse func(*stun.Message) error) *mobilityConn {
	return &mobilityConn{
		PacketConn: conn,
		username:   username,
		password:   password,
		onRequest:  onRequest,
		onResponse: onResponse,
		pending:    make(map[[12]byte]mobilityRequest),
	}
}

func (c *mobilityConn) allocate(client *turn.Client) (net.PacketConn, error) {
	c.mu.Lock()
	wasForbidden := c.forbidden
	c.mu.Unlock()
	conn, err := client.Allocate()
	c.mu.Lock()
	becameForbidden := !wasForbidden && c.forbidden
	c.mu.Unlock()
	if err != nil && becameForbidden {
		// A new Allocate transaction avoids replaying a cached 405 response.
		return client.Allocate()
	}
	return conn, err
}

func (c *mobilityConn) WriteTo(raw []byte, to net.Addr) (int, error) {
	var msg stun.Message
	if !stun.IsMessage(raw) || stun.Decode(raw, &msg) != nil || msg.Type.Class != stun.ClassRequest ||
		(msg.Type.Method != stun.MethodAllocate && msg.Type.Method != stun.MethodRefresh) {
		return c.PacketConn.WriteTo(raw, to)
	}
	request := mobilityRequest{size: len(raw), to: to, method: msg.Type.Method, started: time.Now()}
	if realm, err := msg.Get(stun.AttrRealm); err == nil {
		request.realm = string(realm)
	}
	request.auth = msg.Contains(stun.AttrMessageIntegrity)
	c.mu.Lock()
	for id, pending := range c.pending {
		if request.started.Sub(pending.started) > time.Minute {
			delete(c.pending, id)
		}
	}
	if state, _ := mobilityRelayReports.Load(to.String()); state == "forbidden" {
		c.forbidden = true
	}
	request.offered = msg.Type.Method == stun.MethodAllocate && !c.forbidden
	if request.offered {
		updated, err := c.offerMobility(&msg, request)
		if err != nil {
			c.mu.Unlock()
			return 0, err
		}
		raw = updated
	}
	c.pending[msg.TransactionID] = request
	c.mu.Unlock()
	if err := c.onRequest(&msg); err != nil {
		return 0, err
	}
	_, err := c.PacketConn.WriteTo(raw, to)
	if err != nil {
		return 0, err
	}
	// PacketConn callers expect the length of their original datagram.
	return request.size, nil
}

func (c *mobilityConn) offerMobility(msg *stun.Message, request mobilityRequest) ([]byte, error) {
	updated := &stun.Message{Type: msg.Type, TransactionID: msg.TransactionID}
	updated.WriteHeader()
	for _, attr := range msg.Attributes {
		if attr.Type != stun.AttrMessageIntegrity && attr.Type != stun.AttrFingerprint && attr.Type != attrMobilityTicket {
			updated.Add(attr.Type, attr.Value)
		}
	}
	updated.Add(attrMobilityTicket, nil)
	if request.auth {
		if err := stun.NewLongTermIntegrity(c.username, request.realm, c.password).AddTo(updated); err != nil {
			return nil, err
		}
	}
	if msg.Contains(stun.AttrFingerprint) {
		if err := stun.Fingerprint.AddTo(updated); err != nil {
			return nil, err
		}
	}
	return updated.Raw, nil
}

func (c *mobilityConn) ReadFrom(raw []byte) (int, net.Addr, error) {
	for {
		n, from, err := c.PacketConn.ReadFrom(raw)
		if err != nil {
			return n, from, err
		}
		var msg stun.Message
		if !stun.IsMessage(raw[:n]) || stun.Decode(raw[:n], &msg) != nil {
			return n, from, nil
		}
		c.mu.Lock()
		request, found := c.pending[msg.TransactionID]
		if !found || msg.Type.Method != request.method || from.Network() != request.to.Network() || from.String() != request.to.String() {
			c.mu.Unlock()
			return n, from, nil
		}
		verified := request.auth && stun.NewLongTermIntegrity(c.username, request.realm, c.password).Check(&msg) == nil
		if msg.Type.Class == stun.ClassSuccessResponse && request.auth && !verified {
			c.mu.Unlock()
			continue
		}
		var code stun.ErrorCodeAttribute
		forbidden := msg.Type == stun.NewType(stun.MethodAllocate, stun.ClassErrorResponse) &&
			code.GetFrom(&msg) == nil && code.Code == 405 && request.offered
		if forbidden {
			c.forbidden = true
		}
		if msg.Type.Class == stun.ClassSuccessResponse || msg.Type.Class == stun.ClassErrorResponse {
			delete(c.pending, msg.TransactionID)
		}
		c.mu.Unlock()
		if forbidden {
			previous, _ := mobilityRelayReports.Swap(from.String(), "forbidden")
			if previous != "forbidden" {
				log.Printf("proxy: TURN mobility relay=%s forbidden (405); continuing without mobility", from)
			}
		}
		if verified && (msg.Type.Class == stun.ClassSuccessResponse || msg.Type.Class == stun.ClassErrorResponse) {
			// Attributes after MESSAGE-INTEGRITY are outside its authenticated
			// portion and must never enter the recovery journal.
			for i, attr := range msg.Attributes {
				if attr.Type == stun.AttrMessageIntegrity {
					msg.Attributes = msg.Attributes[:i+1]
					break
				}
			}
			if msg.Type.Method == stun.MethodAllocate && msg.Type.Class == stun.ClassSuccessResponse {
				ticket, _ := msg.Get(attrMobilityTicket)
				state := "unsupported"
				if len(ticket) > 0 {
					state = "supported"
				}
				if _, loaded := mobilityRelayReports.LoadOrStore(from.String(), state); !loaded {
					log.Printf("proxy: TURN mobility relay=%s %s", from, state)
				}
			}
			if err := c.onResponse(&msg); err != nil {
				return 0, from, err
			}
		}
		return n, from, nil
	}
}
