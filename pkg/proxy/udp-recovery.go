package proxy

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"sync"
	"time"

	"github.com/pion/stun/v3"
	"github.com/pion/turn/v5"
)

// Recovery runs before the credential pool consumes its durable release proof.
func prepareAllocationRecovery(ctx context.Context, cachePath string, useUDP bool) (*allocationRecovery, error) {
	path := ""
	if cachePath != "" {
		path = cachePath + ".allocations"
	}
	journal, err := newAllocationRecovery(path)
	if err != nil {
		return nil, fmt.Errorf("load UDP allocation recovery: %w", err)
	}
	if cachePath == "" {
		return journal, nil
	}
	recoveryCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	covered, recoveryErr := journal.recover(recoveryCtx)
	cancel()
	if recoveryErr != nil {
		log.Printf("proxy: UDP recovery incomplete; quota cooldown retained: %v", recoveryErr)
	}
	data, readErr := os.ReadFile(cachePath)
	var cache credCacheFile
	validCache := readErr == nil && json.Unmarshal(data, &cache) == nil && cache.Version == credCacheVersion
	matched := validCache && cache.RecoveryGeneration != "" && cache.RecoveryGeneration == journal.file.Generation
	if covered && matched {
		cache.ReleasedAt = time.Now().UnixNano()
		writer := credPool{cachePath: cachePath}
		if err := writer.writeCache(cache); err != nil {
			return journal, fmt.Errorf("save recovered TURN quota confirmation: %w", err)
		}
		log.Printf("proxy: all journaled UDP allocations released; cached quota can be reused")
	}
	clean := validCache && cache.ReleasedAt > 0 && cache.ReleasedAt <= time.Now().UnixNano()
	if useUDP && !journal.hasPending() && (clean || errors.Is(readErr, os.ErrNotExist)) {
		err = journal.enableCoverage()
		if err == nil {
			if !validCache {
				cache = credCacheFile{Version: credCacheVersion, Mode: credCacheModeNow()}
			}
			cache.RecoveryGeneration = journal.file.Generation
			writer := credPool{cachePath: cachePath}
			err = writer.writeCache(cache)
		}
	} else if !useUDP || !matched {
		err = journal.disableCoverage()
	}
	if err != nil {
		return journal, fmt.Errorf("persist UDP recovery coverage: %w", err)
	}
	return journal, nil
}

func (p *Proxy) trackUDPAllocation(conn net.PacketConn, relay string, creds *TURNCreds) net.PacketConn {
	var mu sync.Mutex
	var record *allocationRecoveryRecord
	onRequest := func(msg *stun.Message) error {
		mu.Lock()
		defer mu.Unlock()
		if p.recoveryErr != nil {
			return p.recoveryErr
		}
		if record == nil {
			var err error
			record, err = p.recovery.begin(conn.LocalAddr().String(), relay, creds)
			if err != nil {
				return fmt.Errorf("journal UDP allocation before sending: %w", err)
			}
		}
		return record.update(func(entry *allocationRecoveryEntry) {
			lifetime, _ := msg.Get(stun.AttrLifetime)
			deleting := msg.Type.Method == stun.MethodRefresh && len(lifetime) == 4 && binary.BigEndian.Uint32(lifetime) == 0
			if !deleting {
				// An unanswered renewal may have extended the server timer.
				entry.ExpiresAt = 0
			}
			if realm, err := msg.Get(stun.AttrRealm); err == nil {
				entry.Realm = string(realm)
			}
			if nonce, err := msg.Get(stun.AttrNonce); err == nil {
				entry.Nonce = string(nonce)
			}
		})
	}
	onResponse := func(msg *stun.Message) error {
		mu.Lock()
		defer mu.Unlock()
		if record == nil {
			return nil
		}
		if msg.Type.Class == stun.ClassErrorResponse {
			var code stun.ErrorCodeAttribute
			if msg.Type.Method == stun.MethodAllocate && code.GetFrom(msg) == nil &&
				code.Code != stun.CodeUnauthorized && code.Code != stun.CodeStaleNonce && code.Code != stun.CodeAllocMismatch {
				if err := record.confirmed(); err != nil {
					return err
				}
				record = nil
			}
			return nil
		}
		lifetime, _ := msg.Get(stun.AttrLifetime)
		if msg.Type.Method == stun.MethodRefresh && len(lifetime) == 4 && lifetime[0]|lifetime[1]|lifetime[2]|lifetime[3] == 0 {
			return record.confirmed()
		}
		return record.update(func(entry *allocationRecoveryEntry) {
			if len(lifetime) == 4 {
				entry.ExpiresAt = time.Now().Add(time.Duration(binary.BigEndian.Uint32(lifetime))*time.Second + time.Second).UnixNano()
			}
			if ticket, err := msg.Get(attrMobilityTicket); err == nil {
				entry.Ticket = append([]byte(nil), ticket...)
			}
			var mapped stun.XORMappedAddress
			if mapped.GetFrom(msg) == nil {
				entry.MappedAddr = net.JoinHostPort(mapped.IP.String(), fmt.Sprint(mapped.Port))
			}
		})
	}
	return wrapMobilityConn(conn, creds.Username, creds.Password, onRequest, onResponse)
}

func (p *Proxy) allocateTURN(client *turn.Client, conn net.PacketConn) (net.PacketConn, error) {
	if p.recoveryErr != nil {
		return nil, p.recoveryErr
	}
	if mobility, ok := conn.(*mobilityConn); ok {
		return mobility.allocate(client)
	}
	return client.Allocate()
}
