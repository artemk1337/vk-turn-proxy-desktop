package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/pion/stun/v3"
)

const allocationRecoveryLimit = 1024

type allocationRecoveryEntry struct {
	ID         string `json:"id"`
	LocalAddr  string `json:"local_addr"`
	Relay      string `json:"relay"`
	Username   string `json:"username"`
	Password   string `json:"password"`
	Realm      string `json:"realm,omitempty"`
	Nonce      string `json:"nonce,omitempty"`
	Ticket     []byte `json:"ticket,omitempty"`
	MappedAddr string `json:"mapped_addr,omitempty"`
	// Zero means that an allocation or lifetime-changing refresh is unanswered.
	ExpiresAt int64 `json:"expires_at,omitempty"`
}

type allocationRecoveryFile struct {
	Version    int    `json:"version"`
	Covered    bool   `json:"covered"`
	Generation string `json:"generation,omitempty"`
	// Retiring unknown allocations disables fast cache reuse while connections continue.
	Untracked bool                      `json:"untracked,omitempty"`
	Entries   []allocationRecoveryEntry `json:"entries"`
}

type allocationRecovery struct {
	mu   sync.Mutex
	path string
	file allocationRecoveryFile
}

type allocationRecoveryRecord struct {
	journal *allocationRecovery
	id      string
}

func newAllocationRecovery(path string) (*allocationRecovery, error) {
	j := &allocationRecovery{path: path, file: allocationRecoveryFile{Version: 1}}
	if path == "" {
		return j, nil
	}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return j, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 1024*1024+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 1024*1024 {
		return nil, errors.New("allocation recovery journal exceeds 1 MiB")
	}
	if err = json.Unmarshal(data, &j.file); err != nil {
		return nil, errors.New("invalid allocation recovery journal")
	}
	if j.file.Version != 1 || len(j.file.Entries) > allocationRecoveryLimit {
		return nil, errors.New("unsupported allocation recovery journal")
	}
	return j, nil
}

func (j *allocationRecovery) saveLocked() error {
	if j.path == "" {
		return nil
	}
	data, err := json.Marshal(j.file)
	if err != nil {
		return err
	}
	if len(data) > 1024*1024 {
		return errors.New("allocation recovery journal exceeds 1 MiB")
	}
	f, err := os.CreateTemp(filepath.Dir(j.path), ".allocation-recovery-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(f.Name(), j.path); err != nil {
		return err
	}
	if dir, openErr := os.Open(filepath.Dir(j.path)); openErr == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}

func (j *allocationRecovery) setCoverage(covered bool) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	previous := j.file.Covered
	j.file.Covered = covered
	if err := j.saveLocked(); err != nil {
		j.file.Covered = previous
		return err
	}
	return nil
}

func (j *allocationRecovery) enableCoverage() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.file.Untracked || len(j.file.Entries) != 0 {
		return errors.New("allocation recovery journal has pending entries")
	}
	previous := j.file.Covered
	previousGeneration := j.file.Generation
	j.file.Covered = true
	j.file.Generation = uuid.NewString()
	if err := j.saveLocked(); err != nil {
		j.file.Covered = previous
		j.file.Generation = previousGeneration
		return err
	}
	return nil
}

func (j *allocationRecovery) disableCoverage() error { return j.setCoverage(false) }

func (j *allocationRecovery) hasPending() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.file.Untracked || len(j.file.Entries) != 0
}

func (j *allocationRecovery) begin(localAddr, relay string, creds *TURNCreds) (*allocationRecoveryRecord, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.path == "" {
		return &allocationRecoveryRecord{journal: j}, nil
	}
	e := allocationRecoveryEntry{ID: uuid.NewString(), LocalAddr: localAddr, Relay: relay, Username: creds.Username, Password: creds.Password}
	previous := j.file
	previous.Entries = append([]allocationRecoveryEntry(nil), j.file.Entries...)
	j.file.Entries = append(j.file.Entries, e)
	retired := false
	for {
		data, err := json.Marshal(j.file)
		if err != nil {
			j.file = previous
			return nil, err
		}
		if len(j.file.Entries) <= allocationRecoveryLimit && len(data) <= 1024*1024 {
			break
		}
		if len(j.file.Entries) == 1 {
			j.file = previous
			return nil, errors.New("allocation recovery entry exceeds journal limit")
		}
		j.file.Entries = j.file.Entries[1:]
		j.file.Covered = false
		j.file.Untracked = true
		retired = true
	}
	if err := j.saveLocked(); err != nil {
		j.file = previous
		return nil, err
	}
	if retired && !previous.Untracked {
		log.Printf("proxy: UDP recovery journal limit reached; oldest records retired and conservative quota waiting retained")
	}
	return &allocationRecoveryRecord{journal: j, id: e.ID}, nil
}

func (r *allocationRecoveryRecord) update(change func(*allocationRecoveryEntry)) error {
	j := r.journal
	j.mu.Lock()
	defer j.mu.Unlock()
	for i := range j.file.Entries {
		if j.file.Entries[i].ID == r.id {
			change(&j.file.Entries[i])
			return j.saveLocked()
		}
	}
	return nil
}

func (r *allocationRecoveryRecord) confirmed() error {
	j := r.journal
	j.mu.Lock()
	defer j.mu.Unlock()
	for i := range j.file.Entries {
		if j.file.Entries[i].ID == r.id {
			previous := append([]allocationRecoveryEntry(nil), j.file.Entries...)
			j.file.Entries = append(j.file.Entries[:i], j.file.Entries[i+1:]...)
			if err := j.saveLocked(); err != nil {
				j.file.Entries = previous
				return err
			}
			return nil
		}
	}
	return nil
}

// recover releases allocations before new sockets can consume their quota.
// A 437 on the original port alone cannot prove that an old NAT mapping is gone.
func (j *allocationRecovery) recover(ctx context.Context) (bool, error) {
	j.mu.Lock()
	entries := append([]allocationRecoveryEntry(nil), j.file.Entries...)
	covered := j.file.Covered && !j.file.Untracked
	j.mu.Unlock()
	var wg sync.WaitGroup
	var resultMu sync.Mutex
	var firstErr error
	slots := make(chan struct{}, 4)
	for _, entry := range entries {
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			wg.Wait()
			return false, ctx.Err()
		}
		wg.Go(func() {
			defer func() { <-slots }()
			confirmed := entry.ExpiresAt > 0 && entry.ExpiresAt <= time.Now().UnixNano()
			var err error
			if !confirmed {
				attemptCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
				confirmed, err = recoverUDPAllocation(attemptCtx, entry)
				cancel()
			}
			if confirmed {
				err = (&allocationRecoveryRecord{journal: j, id: entry.ID}).confirmed()
				log.Printf("proxy: UDP allocation recovery relay=%s allocation released or confirmed lifetime expired", entry.Relay)
			} else {
				log.Printf("proxy: UDP allocation recovery relay=%s release unconfirmed: %v", entry.Relay, err)
			}
			resultMu.Lock()
			defer resultMu.Unlock()
			if err != nil || !confirmed {
				covered = false
			}
			if firstErr == nil && err != nil {
				firstErr = err
			}
		})
	}
	wg.Wait()
	return covered, firstErr
}

func recoverUDPAllocation(ctx context.Context, e allocationRecoveryEntry) (bool, error) {
	local, err := net.ResolveUDPAddr("udp", e.LocalAddr)
	if err != nil {
		return false, err
	}
	relay, err := net.ResolveUDPAddr("udp", e.Relay)
	if err != nil {
		return false, err
	}
	if err = beforeDial(udpNetworkOf(relay), relay.String()); err != nil {
		return false, err
	}
	conn, err := net.ListenUDP(udpNetworkOf(relay), local)
	if err != nil && len(e.Ticket) > 0 {
		conn, err = net.ListenUDP(udpNetworkOf(relay), nil)
	}
	if err != nil {
		return false, err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	deadline, _ := ctx.Deadline()
	_ = conn.SetDeadline(deadline)
	useTicket := conn.LocalAddr().String() != local.String()
	for attempt := 0; attempt < 4; attempt++ {
		setters := []stun.Setter{stun.TransactionID, stun.NewType(stun.MethodRefresh, stun.ClassRequest), stun.RawAttribute{Type: stun.AttrLifetime, Value: []byte{0, 0, 0, 0}}}
		if useTicket {
			setters = append(setters, stun.RawAttribute{Type: stun.AttrType(0x8030), Value: e.Ticket})
		}
		if e.Realm != "" && e.Nonce != "" {
			setters = append(setters, stun.NewUsername(e.Username), stun.NewRealm(e.Realm), stun.NewNonce(e.Nonce), stun.NewLongTermIntegrity(e.Username, e.Realm, e.Password))
		}
		req, buildErr := stun.Build(append(setters, stun.Fingerprint)...)
		if buildErr != nil {
			return false, buildErr
		}
		res, roundErr := recoveryRoundTrip(ctx, conn, relay, req)
		if roundErr != nil {
			return false, roundErr
		}
		var ec stun.ErrorCodeAttribute
		if res.Type == stun.NewType(stun.MethodRefresh, stun.ClassSuccessResponse) {
			if e.Realm == "" || e.Nonce == "" || stun.NewLongTermIntegrity(e.Username, e.Realm, e.Password).Check(res) != nil {
				return false, errors.New("recovery response integrity failed")
			}
			for i, attr := range res.Attributes {
				if attr.Type == stun.AttrMessageIntegrity {
					res.Attributes = res.Attributes[:i+1]
					break
				}
			}
			lifetime, getErr := res.Get(stun.AttrLifetime)
			return getErr == nil && len(lifetime) == 4 && lifetime[0]|lifetime[1]|lifetime[2]|lifetime[3] == 0, nil
		}
		if ec.GetFrom(res) != nil {
			return false, errors.New("invalid recovery response")
		}
		if ec.Code == stun.CodeUnauthorized || ec.Code == stun.CodeStaleNonce {
			var realm stun.Realm
			var nonce stun.Nonce
			if realm.GetFrom(res) != nil || nonce.GetFrom(res) != nil {
				return false, errors.New("invalid recovery challenge")
			}
			e.Realm, e.Nonce = realm.String(), nonce.String()
			continue
		}
		if ec.Code == stun.CodeAllocMismatch && !useTicket && len(e.Ticket) > 0 {
			useTicket = true
			continue
		}
		return false, fmt.Errorf("TURN recovery refused with %d", ec.Code)
	}
	return false, errors.New("TURN recovery challenge limit reached")
}

func recoveryRoundTrip(ctx context.Context, conn *net.UDPConn, relay *net.UDPAddr, req *stun.Message) (*stun.Message, error) {
	deadline, _ := ctx.Deadline()
	buffer := make([]byte, 65535)
	for {
		if _, err := conn.WriteToUDP(req.Raw, relay); err != nil {
			return nil, err
		}
		readUntil := time.Now().Add(200 * time.Millisecond)
		if deadline.Before(readUntil) {
			readUntil = deadline
		}
		if err := conn.SetReadDeadline(readUntil); err != nil {
			return nil, err
		}
		for {
			n, source, err := conn.ReadFromUDP(buffer)
			if err != nil {
				if timeout, ok := err.(net.Error); ok && timeout.Timeout() && time.Now().Before(deadline) {
					break
				}
				return nil, err
			}
			if !source.IP.Equal(relay.IP) || source.Port != relay.Port {
				continue
			}
			res := &stun.Message{Raw: append([]byte(nil), buffer[:n]...)}
			if res.Decode() != nil || res.TransactionID != req.TransactionID {
				continue
			}
			return res, nil
		}
	}
}
