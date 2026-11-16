package proxy

import (
	"errors"
	"net"
	"testing"
	"time"

	"github.com/pion/stun/v3"
	"github.com/pion/turn/v5"
)

func mobilityTestSocket(t *testing.T) net.PacketConn {
	t.Helper()
	conn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := conn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	return conn
}

func mobilityNoop(*stun.Message) error { return nil }

func TestMobilityOfferPreservesIntegrityAndCapturesVerifiedTicket(t *testing.T) {
	server := mobilityTestSocket(t)
	client := mobilityTestSocket(t)
	responses := 0
	conn := wrapMobilityConn(client, "u", "pw", mobilityNoop, func(msg *stun.Message) error {
		ticket, _ := msg.Get(attrMobilityTicket)
		if string(ticket) != "opaque-ticket" {
			t.Errorf("ticket = %q", ticket)
		}
		responses++
		return nil
	})
	request := stun.MustBuild(stun.TransactionID, stun.NewType(stun.MethodAllocate, stun.ClassRequest),
		stun.NewUsername("u"), stun.NewRealm("realm"), stun.NewNonce("nonce"),
		stun.NewLongTermIntegrity("u", "realm", "pw"), stun.Fingerprint)
	n, err := conn.WriteTo(request.Raw, server.LocalAddr())
	if err != nil || n != len(request.Raw) {
		t.Fatalf("write = %d, %v", n, err)
	}
	buffer := make([]byte, 2048)
	n, from, err := server.ReadFrom(buffer)
	if err != nil {
		t.Fatal(err)
	}
	var received stun.Message
	if err := stun.Decode(buffer[:n], &received); err != nil {
		t.Fatal(err)
	}
	ticket, err := received.Get(attrMobilityTicket)
	if err != nil || len(ticket) != 0 {
		t.Fatalf("missing empty mobility offer, ticket=%x, err=%v", ticket, err)
	}
	if err := stun.NewLongTermIntegrity("u", "realm", "pw").Check(&received); err != nil {
		t.Fatal(err)
	}
	if err := stun.Fingerprint.Check(&received); err != nil {
		t.Fatal(err)
	}
	response := stun.MustBuild(stun.NewTransactionIDSetter(request.TransactionID),
		stun.NewType(stun.MethodAllocate, stun.ClassSuccessResponse),
		stun.RawAttribute{Type: attrMobilityTicket, Value: []byte("opaque-ticket")},
		stun.NewLongTermIntegrity("u", "realm", "wrong-password"), stun.Fingerprint)
	if _, err := server.WriteTo(response.Raw, from); err != nil {
		t.Fatal(err)
	}
	response = stun.MustBuild(stun.NewTransactionIDSetter(request.TransactionID),
		stun.NewType(stun.MethodAllocate, stun.ClassSuccessResponse),
		stun.RawAttribute{Type: attrMobilityTicket, Value: []byte("opaque-ticket")},
		stun.NewLongTermIntegrity("u", "realm", "pw"), stun.Fingerprint)
	if _, err := server.WriteTo(response.Raw, from); err != nil {
		t.Fatal(err)
	}
	if _, _, err := conn.ReadFrom(buffer); err != nil {
		t.Fatal(err)
	}
	if responses != 1 {
		t.Fatalf("verified response callbacks = %d", responses)
	}
}

func TestMobilityJournalFailurePreventsRequest(t *testing.T) {
	client := mobilityTestSocket(t)
	persistErr := errors.New("journal unavailable")
	conn := wrapMobilityConn(client, "u", "pw", func(*stun.Message) error { return persistErr }, mobilityNoop)
	request := stun.MustBuild(stun.TransactionID, stun.NewType(stun.MethodAllocate, stun.ClassRequest))
	if _, err := conn.WriteTo(request.Raw, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1}); !errors.Is(err, persistErr) {
		t.Fatalf("write error = %v", err)
	}
}

func TestMobilityForbiddenRetriesWithNewTransaction(t *testing.T) {
	server := mobilityTestSocket(t)
	client := mobilityTestSocket(t)
	conn := wrapMobilityConn(client, "u", "pw", mobilityNoop, mobilityNoop)
	tc, err := turn.NewClient(&turn.ClientConfig{Conn: conn, TURNServerAddr: server.LocalAddr().String(), Username: "u", Password: "pw"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(tc.Close)
	if err := tc.Listen(); err != nil {
		t.Fatal(err)
	}
	requests := make(chan *stun.Message, 8)
	go func() {
		buffer := make([]byte, 2048)
		for {
			n, from, err := server.ReadFrom(buffer)
			if err != nil {
				return
			}
			request := &stun.Message{}
			if stun.Decode(append([]byte(nil), buffer[:n]...), request) != nil {
				return
			}
			requests <- request
			response := stun.MustBuild(stun.NewTransactionIDSetter(request.TransactionID),
				stun.NewType(request.Type.Method, stun.ClassErrorResponse))
			switch {
			case request.Contains(attrMobilityTicket):
				_ = (&stun.ErrorCodeAttribute{Code: 405, Reason: []byte("Mobility Forbidden")}).AddTo(response)
			case !request.Contains(stun.AttrMessageIntegrity):
				_ = (&stun.ErrorCodeAttribute{Code: stun.CodeUnauthorized}).AddTo(response)
				_ = stun.NewRealm("realm").AddTo(response)
				_ = stun.NewNonce("nonce").AddTo(response)
			default:
				response.Type.Class = stun.ClassSuccessResponse
				response.WriteType()
				lifetime := []byte{0, 0, 2, 88}
				if request.Type.Method == stun.MethodRefresh {
					lifetime = []byte{0, 0, 0, 0}
				}
				response.Add(stun.AttrLifetime, lifetime)
				_ = (stun.XORMappedAddress{IP: net.IPv4(127, 0, 0, 1), Port: 9000}).AddToAs(response, stun.AttrXORRelayedAddress)
				_ = stun.NewLongTermIntegrity("u", "realm", "pw").AddTo(response)
			}
			if _, err := server.WriteTo(response.Raw, from); err != nil {
				return
			}
		}
	}()
	relay, err := conn.allocate(tc)
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()
	first, second, third := <-requests, <-requests, <-requests
	if !first.Contains(attrMobilityTicket) || second.Contains(attrMobilityTicket) || third.Contains(attrMobilityTicket) {
		t.Fatal("mobility negotiation was not disabled after 405")
	}
	if first.TransactionID == second.TransactionID {
		t.Fatal("fallback reused the rejected transaction ID")
	}
}
