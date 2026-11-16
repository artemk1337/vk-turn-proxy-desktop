package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeReleasedCache(t *testing.T, mode string, releasedAt time.Time) string {
	t.Helper()
	used := time.Now().Add(-time.Minute).Unix()
	file := credCacheFile{
		Version: credCacheVersion,
		SavedAt: time.Now().Unix(),
		Mode:    mode,
		Creds: []credCacheEntry{{
			Slot:       0,
			Address:    relayA,
			Addresses:  []string{relayA},
			Username:   futureUser("released-cache"),
			Password:   "p",
			LastUsedAt: used,
			UsedOn:     map[string]int64{relayA: used},
		}},
	}
	if !releasedAt.IsZero() {
		file.ReleasedAt = releasedAt.UnixNano()
	}
	if mode == credCacheModeCookie {
		file.RelayUsedAt = map[string]int64{relayA: used}
	}
	path := filepath.Join(t.TempDir(), "credentials.json")
	data, err := json.Marshal(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func readReleasedCache(t *testing.T, path string) credCacheFile {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var file credCacheFile
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	return file
}

func loadReleasedPool(t *testing.T, path, mode string) *credPool {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	// Load without a periodic saver so a simulated crash leaves exactly the
	// last durable snapshot, including a crash before the first minute tick.
	cp := newCredPool(ctx, 1, time.Minute, "", func(bool, int) (string, *TURNCreds, error) { return "", nil, errors.New("unexpected fetch") })
	cp.mode = mode
	cp.cachePath = path
	cp.loadFromDisk()
	return cp
}

func TestCachedCredentialsRespectUnconfirmedCooldown(t *testing.T) {
	for _, mode := range []string{credCacheModeAnon, credCacheModeCookie} {
		for _, releasedAt := range []time.Time{{}, time.Now(), time.Now().Add(time.Hour)} {
			t.Run(mode+releasedAt.Format(time.RFC3339Nano), func(t *testing.T) {
				path := writeReleasedCache(t, mode, releasedAt)
				cp := loadReleasedPool(t, path, mode)
				cp.mu.Lock()
				ready := cp.freshOnLocked(0, relayA)
				cp.mu.Unlock()
				confirmed := !releasedAt.IsZero() && !releasedAt.After(time.Now())
				if ready != confirmed {
					t.Fatalf("ready=%v, confirmed=%v", ready, confirmed)
				}
				_, _, slot, _, err := cp.handOut(0, false)
				if confirmed {
					if err != nil || slot != 0 {
						t.Fatalf("confirmed acquisition = %d, %v", slot, err)
					}
					cp.saveToDisk()
					restarted := loadReleasedPool(t, path, mode)
					if restarted.isFreshLocked(0) {
						t.Fatal("interrupted reuse lost its cooldown")
					}
				} else {
					var cooldown *QuotaCooldownError
					if !errors.As(err, &cooldown) || time.Until(cooldown.Until) > credSaturationCooldown || time.Until(cooldown.Until) < 8*time.Minute {
						t.Fatalf("cooldown = %v", err)
					}
				}
				info, err := os.Stat(path)
				if err != nil || info.Mode().Perm() != 0o600 {
					t.Fatalf("cache permissions = %v, error %v", info, err)
				}
			})
		}
	}
}

func TestCachedCredentialsRequireDurableDirtyBarrier(t *testing.T) {
	path := writeReleasedCache(t, credCacheModeAnon, time.Now())
	cp := loadReleasedPool(t, path, credCacheModeAnon)
	cp.cachePath = filepath.Join(t.TempDir(), "missing", "cache.json")
	cp.pendingRelease = true
	if _, _, _, _, err := cp.handOut(0, false); err == nil {
		t.Fatal("allocation ignored failed dirty-cache barrier")
	}
}

func TestStoppedCacheKeepsCredentialsWithoutReleaseProof(t *testing.T) {
	path := writeReleasedCache(t, credCacheModeAnon, time.Now())
	cp := loadReleasedPool(t, path, credCacheModeAnon)
	p := &Proxy{credPool: cp}
	p.dealloc.unconfirmed.Store(1)
	p.saveStoppedCache()
	file := readReleasedCache(t, path)
	if file.ReleasedAt != 0 || len(file.Creds) != 1 {
		t.Fatalf("final cache = %+v", file)
	}
	if loadReleasedPool(t, path, credCacheModeAnon).isFreshLocked(0) {
		t.Fatal("unconfirmed shutdown lost its cooldown")
	}
}
