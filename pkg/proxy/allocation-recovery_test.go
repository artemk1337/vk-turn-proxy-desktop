package proxy

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pion/stun/v3"
)

func TestAllocationRecoveryJournal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "allocations.json")
	journal, err := newAllocationRecovery(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = journal.enableCoverage(); err != nil {
		t.Fatal(err)
	}
	record, err := journal.begin("127.0.0.1:43210", "127.0.0.1:3478", &TURNCreds{Username: "user", Password: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	restored, err := newAllocationRecovery(path)
	if err != nil {
		t.Fatal(err)
	}
	if !restored.file.Covered || !restored.hasPending() {
		t.Fatal("recovery intent was not persisted")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("journal must be private", err)
	}
	journal.path = filepath.Join(t.TempDir(), "missing", "allocations.json")
	if record.confirmed() == nil || !journal.hasPending() {
		t.Fatal("failed delete must preserve pending record")
	}
	journal.path = path
	if err = record.confirmed(); err != nil {
		t.Fatal(err)
	}
	restored, err = newAllocationRecovery(path)
	if err != nil || restored.hasPending() {
		t.Fatal("confirmed release was not persisted", err)
	}
}

func TestAllocationRecoveryOriginalUDPPort(t *testing.T) {
	for _, invalidIntegrity := range []bool{false, true} {
		t.Run(map[bool]string{false: "confirmed", true: "invalid-integrity"}[invalidIntegrity], func(t *testing.T) {
			server, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
			if err != nil {
				t.Fatal(err)
			}
			defer server.Close()
			prior, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
			if err != nil {
				t.Fatal(err)
			}
			local := prior.LocalAddr().String()
			_ = prior.Close()
			seen := make(chan string, 1)
			go func() {
				buffer := make([]byte, 2048)
				n, addr, readErr := server.ReadFromUDP(buffer)
				if readErr != nil {
					return
				}
				seen <- addr.String()
				req := &stun.Message{Raw: buffer[:n]}
				if req.Decode() != nil {
					return
				}
				password := "secret"
				if invalidIntegrity {
					password = "wrong"
				}
				res := stun.MustBuild(stun.NewTransactionIDSetter(req.TransactionID), stun.NewType(stun.MethodRefresh, stun.ClassSuccessResponse), stun.RawAttribute{Type: stun.AttrLifetime, Value: []byte{0, 0, 0, 0}}, stun.NewLongTermIntegrity("user", "realm", password), stun.Fingerprint)
				_, _ = server.WriteToUDP(res.Raw, addr)
			}()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			confirmed, err := recoverUDPAllocation(ctx, allocationRecoveryEntry{LocalAddr: local, Relay: server.LocalAddr().String(), Username: "user", Password: "secret", Realm: "realm", Nonce: "nonce"})
			if confirmed == invalidIntegrity {
				t.Fatalf("confirmed=%v err=%v", confirmed, err)
			}
			if got := <-seen; got != local {
				t.Fatalf("source=%s want %s", got, local)
			}
		})
	}
}

func TestAllocationRecoveryMobilityAfterMismatch(t *testing.T) {
	server, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	old, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	local := old.LocalAddr().String()
	_ = old.Close()
	ticketSeen := make(chan bool, 1)
	go func() {
		buffer := make([]byte, 2048)
		for i := 0; i < 2; i++ {
			n, addr, readErr := server.ReadFromUDP(buffer)
			if readErr != nil {
				return
			}
			req := &stun.Message{Raw: append([]byte(nil), buffer[:n]...)}
			if req.Decode() != nil {
				return
			}
			setters := []stun.Setter{stun.NewTransactionIDSetter(req.TransactionID)}
			if i == 0 {
				setters = append(setters, stun.NewType(stun.MethodRefresh, stun.ClassErrorResponse), stun.ErrorCodeAttribute{Code: stun.CodeAllocMismatch})
			} else {
				ticket, getErr := req.Get(stun.AttrType(0x8030))
				ticketSeen <- getErr == nil && string(ticket) == "ticket"
				setters = append(setters, stun.NewType(stun.MethodRefresh, stun.ClassSuccessResponse), stun.RawAttribute{Type: stun.AttrLifetime, Value: []byte{0, 0, 0, 0}}, stun.NewLongTermIntegrity("user", "realm", "secret"))
			}
			res := stun.MustBuild(setters...)
			_, _ = server.WriteToUDP(res.Raw, addr)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	confirmed, err := recoverUDPAllocation(ctx, allocationRecoveryEntry{LocalAddr: local, Relay: server.LocalAddr().String(), Username: "user", Password: "secret", Realm: "realm", Nonce: "nonce", Ticket: []byte("ticket")})
	if err != nil || !confirmed {
		t.Fatalf("confirmed=%v err=%v", confirmed, err)
	}
	if !<-ticketSeen {
		t.Fatal("mobility ticket was not sent")
	}
}

func TestAllocationRecoveryMismatchKeepsJournal(t *testing.T) {
	server, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	prior, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	local := prior.LocalAddr().String()
	_ = prior.Close()
	journal, err := newAllocationRecovery(filepath.Join(t.TempDir(), "allocations.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = journal.enableCoverage(); err != nil {
		t.Fatal(err)
	}
	if _, err = journal.begin(local, server.LocalAddr().String(), &TURNCreds{Username: "user", Password: "secret"}); err != nil {
		t.Fatal(err)
	}
	go func() {
		buffer := make([]byte, 2048)
		n, addr, readErr := server.ReadFromUDP(buffer)
		if readErr != nil {
			return
		}
		req := &stun.Message{Raw: buffer[:n]}
		if req.Decode() != nil {
			return
		}
		res := stun.MustBuild(stun.NewTransactionIDSetter(req.TransactionID), stun.NewType(stun.MethodRefresh, stun.ClassErrorResponse), stun.ErrorCodeAttribute{Code: stun.CodeAllocMismatch})
		_, _ = server.WriteToUDP(res.Raw, addr)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	covered, err := journal.recover(ctx)
	if err == nil || covered || !journal.hasPending() {
		t.Fatalf("437 must retain uncertainty, covered=%v pending=%v err=%v", covered, journal.hasPending(), err)
	}
}

func TestAllocationRecoveryKnownExpiry(t *testing.T) {
	journal, err := newAllocationRecovery(filepath.Join(t.TempDir(), "allocations.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = journal.enableCoverage(); err != nil {
		t.Fatal(err)
	}
	record, err := journal.begin("invalid socket", "invalid relay", &TURNCreds{Username: "user", Password: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	if err = record.update(func(entry *allocationRecoveryEntry) { entry.ExpiresAt = time.Now().Add(-time.Second).UnixNano() }); err != nil {
		t.Fatal(err)
	}
	covered, err := journal.recover(context.Background())
	if err != nil || !covered || journal.hasPending() {
		t.Fatalf("known expiry must settle recovery, covered=%v pending=%v err=%v", covered, journal.hasPending(), err)
	}

	record, err = journal.begin("invalid socket", "invalid relay", &TURNCreds{Username: "user", Password: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	if err = record.update(func(entry *allocationRecoveryEntry) { entry.ExpiresAt = 0 }); err != nil {
		t.Fatal(err)
	}
	covered, err = journal.recover(context.Background())
	if err == nil || covered || !journal.hasPending() {
		t.Fatalf("unknown lifetime must remain pending, covered=%v pending=%v err=%v", covered, journal.hasPending(), err)
	}
}

func TestAllocationRecoveryOverflowRetainsConservativeMarker(t *testing.T) {
	for _, bySize := range []bool{false, true} {
		t.Run(map[bool]string{false: "record-count", true: "encoded-size"}[bySize], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "allocations.json")
			journal, err := newAllocationRecovery(path)
			if err != nil {
				t.Fatal(err)
			}
			journal.file.Covered = true
			count := allocationRecoveryLimit
			password := "pw"
			if bySize {
				count = 2
				password = strings.Repeat("x", 400000)
			}
			for i := 0; i < count; i++ {
				journal.file.Entries = append(journal.file.Entries, allocationRecoveryEntry{ID: fmt.Sprint(i), Password: password})
			}
			if err = journal.saveLocked(); err != nil {
				t.Fatal(err)
			}
			record, err := journal.begin("127.0.0.1:43210", "127.0.0.1:3478", &TURNCreds{Username: "u", Password: password})
			if err != nil {
				t.Fatal(err)
			}
			restored, err := newAllocationRecovery(path)
			if err != nil {
				t.Fatal(err)
			}
			if !restored.file.Untracked || restored.file.Covered || restored.file.Entries[0].ID == "0" {
				t.Fatal("overflow must retire oldest and retain uncertainty")
			}
			if len(restored.file.Entries) > allocationRecoveryLimit {
				t.Fatal("record bound exceeded")
			}
			if restored.enableCoverage() == nil {
				t.Fatal("retired unknown allocations must prevent coverage")
			}
			for _, entry := range append([]allocationRecoveryEntry(nil), journal.file.Entries...) {
				if err = (&allocationRecoveryRecord{journal: journal, id: entry.ID}).confirmed(); err != nil {
					t.Fatal(err)
				}
			}
			if !journal.hasPending() {
				t.Fatal("retired unknown allocations must remain pending")
			}
			covered, err := journal.recover(context.Background())
			if covered || err != nil {
				t.Fatalf("covered=%v err=%v", covered, err)
			}
			if err = record.confirmed(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAllocationRecoveryOverflowRollback(t *testing.T) {
	journal, err := newAllocationRecovery(filepath.Join(t.TempDir(), "allocations.json"))
	if err != nil {
		t.Fatal(err)
	}
	journal.file.Covered = true
	for i := 0; i < allocationRecoveryLimit; i++ {
		journal.file.Entries = append(journal.file.Entries, allocationRecoveryEntry{ID: fmt.Sprint(i)})
	}
	journal.path = filepath.Join(t.TempDir(), "missing", "allocations.json")
	if _, err = journal.begin("local", "relay", &TURNCreds{Username: "u", Password: "pw"}); err == nil {
		t.Fatal("expected persistence failure")
	}
	if journal.file.Untracked || !journal.file.Covered || len(journal.file.Entries) != allocationRecoveryLimit || journal.file.Entries[0].ID != "0" {
		t.Fatal("failed write must roll back full coverage state and entries")
	}
}

func TestAllocationRecoveryRejectsSingleOversizedEntry(t *testing.T) {
	journal, err := newAllocationRecovery(filepath.Join(t.TempDir(), "allocations.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = journal.begin("local", "relay", &TURNCreds{Username: "u", Password: strings.Repeat("x", 1024*1024)}); err == nil {
		t.Fatal("single oversized entry must be rejected")
	}
	if journal.hasPending() || journal.file.Untracked {
		t.Fatal("failed oversized entry must not change journal")
	}
}

func TestAllocationRecoveryCoverageGeneration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "allocations.json")
	journal, err := newAllocationRecovery(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = journal.enableCoverage(); err != nil {
		t.Fatal(err)
	}
	first := journal.file.Generation
	if first == "" {
		t.Fatal("coverage requires a generation")
	}
	if err = journal.enableCoverage(); err != nil {
		t.Fatal(err)
	}
	second := journal.file.Generation
	if second == first || second == "" {
		t.Fatal("new coverage must rotate generation")
	}
	restored, err := newAllocationRecovery(path)
	if err != nil || restored.file.Generation != second {
		t.Fatal("generation must persist", err)
	}
	journal.path = filepath.Join(t.TempDir(), "missing", "allocations.json")
	if journal.enableCoverage() == nil || journal.file.Generation != second {
		t.Fatal("failed write must preserve previous generation")
	}
}
