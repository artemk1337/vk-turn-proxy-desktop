package proxy

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/pion/stun/v3"
	"github.com/pion/turn/v5"
)

func TestPrepareAllocationRecoveryCoverage(t *testing.T) {
	for _, covered := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "covered"}[covered], func(t *testing.T) {
			path := writeReleasedCache(t, credCacheModeCookie, time.Time{})
			journal, err := newAllocationRecovery(path + ".allocations")
			if err != nil {
				t.Fatal(err)
			}
			if covered {
				if err = journal.enableCoverage(); err != nil {
					t.Fatal(err)
				}
				cache := readReleasedCache(t, path)
				cache.RecoveryGeneration = journal.file.Generation
				writer := credPool{cachePath: path}
				if err = writer.writeCache(cache); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = prepareAllocationRecovery(context.Background(), path, true); err != nil {
				t.Fatal(err)
			}
			if got := readReleasedCache(t, path).ReleasedAt > 0; got != covered {
				t.Fatalf("release proof=%v want %v", got, covered)
			}
		})
	}
}

func TestPrepareAllocationRecoveryTCPDisablesCoverage(t *testing.T) {
	path := writeReleasedCache(t, credCacheModeCookie, time.Now())
	journal, err := newAllocationRecovery(path + ".allocations")
	if err != nil {
		t.Fatal(err)
	}
	if err = journal.enableCoverage(); err != nil {
		t.Fatal(err)
	}
	if _, err = prepareAllocationRecovery(context.Background(), path, false); err != nil {
		t.Fatal(err)
	}
	restored, err := newAllocationRecovery(path + ".allocations")
	if err != nil {
		t.Fatal(err)
	}
	if restored.file.Covered {
		t.Fatal("TCP allocations must invalidate UDP-only coverage")
	}
}

func TestPrepareAllocationRecoveryRejectsUnlinkedCache(t *testing.T) {
	path := writeReleasedCache(t, credCacheModeCookie, time.Time{})
	journal, err := newAllocationRecovery(path + ".allocations")
	if err != nil {
		t.Fatal(err)
	}
	if err = journal.enableCoverage(); err != nil {
		t.Fatal(err)
	}
	// An older client can rewrite the cache without participating in recovery.
	if _, err = prepareAllocationRecovery(context.Background(), path, true); err != nil {
		t.Fatal(err)
	}
	if readReleasedCache(t, path).ReleasedAt != 0 {
		t.Fatal("an unrelated cache write must not inherit journal proof")
	}
	restored, err := newAllocationRecovery(path + ".allocations")
	if err != nil || restored.file.Covered {
		t.Fatal("unlinked cache must invalidate coverage", err)
	}
}

func TestRecoveryGenerationSurvivesCredentialCacheWrites(t *testing.T) {
	path := writeReleasedCache(t, credCacheModeCookie, time.Now())
	journal, err := prepareAllocationRecovery(context.Background(), path, true)
	if err != nil {
		t.Fatal(err)
	}
	pool := loadReleasedPool(t, path, credCacheModeCookie)
	pool.saveToDisk()
	cache := readReleasedCache(t, path)
	if cache.RecoveryGeneration == "" || cache.RecoveryGeneration != journal.file.Generation {
		t.Fatal("credential snapshot lost its recovery generation")
	}
	if cache.ReleasedAt != 0 {
		t.Fatal("the release proof must still be consumed before reuse")
	}
}

func TestPrepareAllocationRecoveryPendingEntriesPreventCoverage(t *testing.T) {
	path := writeReleasedCache(t, credCacheModeCookie, time.Time{})
	journal, err := newAllocationRecovery(path + ".allocations")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = journal.begin("invalid socket", "invalid relay", &TURNCreds{Username: "u", Password: "pw"}); err != nil {
		t.Fatal(err)
	}
	if journal.enableCoverage() == nil {
		t.Fatal("pending entries must block establishing coverage")
	}
	restored, err := prepareAllocationRecovery(context.Background(), path, true)
	if err != nil {
		t.Fatal(err)
	}
	if restored.file.Covered || !restored.hasPending() || readReleasedCache(t, path).ReleasedAt != 0 {
		t.Fatal("pending legacy allocation lost quota uncertainty")
	}
}

func TestPrepareAllocationRecoveryInvalidJournalBlocksAllocation(t *testing.T) {
	path := writeReleasedCache(t, credCacheModeCookie, time.Time{})
	if err := os.WriteFile(path+".allocations", []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := prepareAllocationRecovery(context.Background(), path, true)
	if err == nil {
		t.Fatal("invalid journal must report failure")
	}
	p := &Proxy{recoveryErr: err}
	if _, allocateErr := p.allocateTURN(&turn.Client{}, mobilityTestSocket(t)); allocateErr == nil {
		t.Fatal("allocation must fail before sending when journal is unavailable")
	}
}

func TestTrackUDPAllocationPersistsVerifiedLifetimeAndRenewalIntent(t *testing.T) {
	server := mobilityTestSocket(t)
	client := mobilityTestSocket(t)
	path := writeReleasedCache(t, credCacheModeCookie, time.Time{}) + ".allocations"
	journal, err := newAllocationRecovery(path)
	if err != nil {
		t.Fatal(err)
	}
	p := &Proxy{recovery: journal}
	conn := p.trackUDPAllocation(client, server.LocalAddr().String(), &TURNCreds{Username: "u", Password: "pw"})
	request := stun.MustBuild(stun.TransactionID, stun.NewType(stun.MethodAllocate, stun.ClassRequest), stun.NewUsername("u"), stun.NewRealm("realm"), stun.NewNonce("nonce"), stun.NewLongTermIntegrity("u", "realm", "pw"))
	if _, err = conn.WriteTo(request.Raw, server.LocalAddr()); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 2048)
	_, source, err := server.ReadFrom(buffer)
	if err != nil {
		t.Fatal(err)
	}
	response := stun.MustBuild(stun.NewTransactionIDSetter(request.TransactionID), stun.NewType(stun.MethodAllocate, stun.ClassSuccessResponse), stun.RawAttribute{Type: stun.AttrLifetime, Value: []byte{0, 0, 0, 60}}, stun.RawAttribute{Type: attrMobilityTicket, Value: []byte("opaque")}, stun.NewLongTermIntegrity("u", "realm", "pw"))
	before := time.Now()
	if _, err = server.WriteTo(response.Raw, source); err != nil {
		t.Fatal(err)
	}
	if _, _, err = conn.ReadFrom(buffer); err != nil {
		t.Fatal(err)
	}
	restored, err := newAllocationRecovery(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored.file.Entries) != 1 {
		t.Fatal("missing allocation record")
	}
	entry := restored.file.Entries[0]
	if string(entry.Ticket) != "opaque" || entry.Realm != "realm" || entry.Nonce != "nonce" || entry.ExpiresAt < before.Add(60*time.Second).UnixNano() {
		t.Fatal("verified allocation metadata was not persisted")
	}
	deleting := stun.MustBuild(stun.TransactionID, stun.NewType(stun.MethodRefresh, stun.ClassRequest), stun.RawAttribute{Type: stun.AttrLifetime, Value: []byte{0, 0, 0, 0}}, stun.NewUsername("u"), stun.NewRealm("realm"), stun.NewNonce("nonce"), stun.NewLongTermIntegrity("u", "realm", "pw"))
	if _, err = conn.WriteTo(deleting.Raw, server.LocalAddr()); err != nil {
		t.Fatal(err)
	}
	restored, err = newAllocationRecovery(path)
	if err != nil || restored.file.Entries[0].ExpiresAt != entry.ExpiresAt {
		t.Fatal("deletion intent must preserve known expiry", err)
	}
	renewing := stun.MustBuild(stun.TransactionID, stun.NewType(stun.MethodRefresh, stun.ClassRequest), stun.RawAttribute{Type: stun.AttrLifetime, Value: []byte{0, 0, 0, 60}}, stun.NewUsername("u"), stun.NewRealm("realm"), stun.NewNonce("nonce"), stun.NewLongTermIntegrity("u", "realm", "pw"))
	if _, err = conn.WriteTo(renewing.Raw, server.LocalAddr()); err != nil {
		t.Fatal(err)
	}
	restored, err = newAllocationRecovery(path)
	if err != nil || restored.file.Entries[0].ExpiresAt != 0 {
		t.Fatal("unanswered renewal must invalidate known expiry", err)
	}
}
