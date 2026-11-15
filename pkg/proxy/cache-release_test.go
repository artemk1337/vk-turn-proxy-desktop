package proxy

import (
	"context"
	"encoding/json"
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
	cp := newCredPool(ctx, 2, time.Minute, "", nil)
	cp.mode = mode
	cp.cachePath = path
	cp.loadFromDisk()
	return cp
}

func TestReleasedCacheReconnectsAndConsumesProofBeforeAllocation(t *testing.T) {
	for _, mode := range []string{credCacheModeAnon, credCacheModeCookie} {
		t.Run(mode, func(t *testing.T) {
			path := writeReleasedCache(t, mode, time.Now().Add(-3*time.Second))
			cp := loadReleasedPool(t, path, mode)
			cp.mu.Lock()
			ready := cp.freshOnLocked(0, relayA)
			cp.mu.Unlock()
			if !ready {
				t.Fatal("confirmed release did not make the cached credential ready")
			}
			dirty := readReleasedCache(t, path)
			if dirty.ReleasedAt != 0 {
				t.Fatal("clean proof remained on disk before a new allocation")
			}
			// The second process crashed before allocating or periodically
			// saving. The next process must still treat the relay as in use.
			restarted := loadReleasedPool(t, path, mode)
			restarted.mu.Lock()
			ready = restarted.freshOnLocked(0, relayA)
			restarted.mu.Unlock()
			if ready {
				t.Fatal("a crash immediately after consuming the proof bypassed cooldown")
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != 0o600 {
				t.Fatalf("cache permissions = %o, want 600", info.Mode().Perm())
			}
		})
	}
}

func TestDirtyCacheRetainsItsPreviousCooldown(t *testing.T) {
	for _, mode := range []string{credCacheModeAnon, credCacheModeCookie} {
		t.Run(mode, func(t *testing.T) {
			path := writeReleasedCache(t, mode, time.Time{})
			before := readReleasedCache(t, path)
			cp := loadReleasedPool(t, path, mode)
			cp.mu.Lock()
			ready := cp.freshOnLocked(0, relayA)
			cp.mu.Unlock()
			if ready {
				t.Fatal("legacy cache bypassed the existing cooldown")
			}
			after := readReleasedCache(t, path)
			if after.Creds[0].LastUsedAt != before.Creds[0].LastUsedAt {
				t.Fatal("loading a legacy cache moved its previous cooldown")
			}
		})
	}
}

func TestInheritedUncertaintyPreventsCleanCacheProof(t *testing.T) {
	for _, mode := range []string{credCacheModeAnon, credCacheModeCookie} {
		t.Run(mode, func(t *testing.T) {
			path := writeReleasedCache(t, mode, time.Time{})
			cp := loadReleasedPool(t, path, mode)
			if !cp.inheritedUntil.After(time.Now()) {
				t.Fatal("recent cache use was not retained as inherited uncertainty")
			}
			cp.saveCache(time.Now())
			if file := readReleasedCache(t, path); file.ReleasedAt != 0 {
				t.Fatal("a newly confirmed shutdown erased prior unknown allocations")
			}
		})
	}
}

func TestReleasedCacheWriteFailureFallsBackToCooldown(t *testing.T) {
	for _, mode := range []string{credCacheModeAnon, credCacheModeCookie} {
		t.Run(mode, func(t *testing.T) {
			path := writeReleasedCache(t, mode, time.Now().Add(-3*time.Second))
			blocked := path + ".tmp"
			if err := os.Mkdir(blocked, 0o700); err != nil {
				t.Fatal(err)
			}
			// A nonempty directory cannot be removed by the atomic writer's
			// best-effort cleanup, regardless of the test user's privileges.
			if err := os.WriteFile(filepath.Join(blocked, "keep"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			cp := loadReleasedPool(t, path, mode)
			cp.mu.Lock()
			ready := cp.freshOnLocked(0, relayA)
			cp.mu.Unlock()
			if ready {
				t.Fatal("clean proof was trusted without durable invalidation")
			}
			if file := readReleasedCache(t, path); file.ReleasedAt == 0 {
				t.Fatal("failed invalidation unexpectedly replaced the original cache")
			}
		})
	}
}

func TestOrdinarySaveAfterCleanRestartKeepsCrashProtection(t *testing.T) {
	for _, mode := range []string{credCacheModeAnon, credCacheModeCookie} {
		t.Run(mode, func(t *testing.T) {
			path := writeReleasedCache(t, mode, time.Now().Add(-3*time.Second))
			cp := loadReleasedPool(t, path, mode)
			cp.saveToDisk()
			if file := readReleasedCache(t, path); file.ReleasedAt != 0 {
				t.Fatal("ordinary save restored clean shutdown proof")
			}
			restarted := loadReleasedPool(t, path, mode)
			restarted.mu.Lock()
			ready := restarted.freshOnLocked(0, relayA)
			restarted.mu.Unlock()
			if ready {
				t.Fatal("ordinary save erased the write-ahead crash protection")
			}
		})
	}
}

func TestExpiredInheritedUncertaintyAllowsCleanCacheProof(t *testing.T) {
	path := writeReleasedCache(t, credCacheModeCookie, time.Time{})
	cp := loadReleasedPool(t, path, credCacheModeCookie)
	cp.mu.Lock()
	cp.inheritedUntil = time.Now().Add(-time.Second)
	cp.mu.Unlock()
	releasedAt := time.Now()
	cp.saveCache(releasedAt)
	if file := readReleasedCache(t, path); file.ReleasedAt != releasedAt.UnixNano() {
		t.Fatalf("release proof = %d, want %d", file.ReleasedAt, releasedAt.UnixNano())
	}
}

func TestReleasedCachePreservesTheRelaysShortReleaseDelay(t *testing.T) {
	for _, mode := range []string{credCacheModeAnon, credCacheModeCookie} {
		t.Run(mode, func(t *testing.T) {
			releasedAt := time.Now()
			path := writeReleasedCache(t, mode, releasedAt)
			cp := loadReleasedPool(t, path, mode)
			cp.mu.Lock()
			readyAt := cp.clockLocked(0, relayA)
			cp.mu.Unlock()
			want := releasedAt.Add(seatCoolFor)
			if !readyAt.Equal(want) {
				t.Fatalf("ready at %s, want confirmed release plus relay delay %s", readyAt, want)
			}
		})
	}
}

func cacheShutdownProxy(t *testing.T) (*Proxy, string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cp := newCredPool(ctx, 2, time.Minute, "", nil)
	cp.mode = credCacheModeAnon
	cp.cachePath = filepath.Join(t.TempDir(), "credentials.json")
	cp.pool[0] = credPoolEntry{
		addr:      relayA,
		addrs:     []string{relayA},
		creds:     &TURNCreds{Username: futureUser("stopped-cache"), Password: "p", Address: relayA, Addresses: []string{relayA}},
		ts:        time.Now(),
		relayHist: relayHist{lastUsedAt: time.Now()},
	}
	return &Proxy{ctx: ctx, cancel: cancel, credPool: cp}, cp.cachePath
}

func TestProxyStopPersistsOnlyFullyConfirmedRelease(t *testing.T) {
	cases := []struct {
		name        string
		started     int64
		confirmed   int64
		unconfirmed int64
		legsLeft    int64
		clean       bool
	}{
		{name: "all-confirmed", started: 2, confirmed: 2, clean: true},
		{name: "allocate-result-unknown", started: 2, confirmed: 1},
		{name: "deallocate-unconfirmed", started: 2, confirmed: 2, unconfirmed: 1},
		{name: "relay-leg-left-running", started: 2, confirmed: 2, legsLeft: 1},
	}
	methods := []struct {
		name string
		stop func(*Proxy)
	}{
		{name: "Stop", stop: func(p *Proxy) { p.Stop() }},
		{name: "StopWithTimeout", stop: func(p *Proxy) { p.StopWithTimeout(time.Second) }},
	}
	for _, method := range methods {
		for _, test := range cases {
			t.Run(method.name+"/"+test.name, func(t *testing.T) {
				p, path := cacheShutdownProxy(t)
				p.dealloc.started.Store(test.started)
				p.dealloc.confirmed.Store(test.confirmed)
				p.dealloc.unconfirmed.Store(test.unconfirmed)
				p.dealloc.legsLeft.Store(test.legsLeft)
				p.wg.Add(1)
				go func() {
					defer p.wg.Done()
					<-p.ctx.Done()
				}()
				method.stop(p)
				clean := readReleasedCache(t, path).ReleasedAt > 0
				if clean != test.clean {
					t.Fatalf("clean shutdown proof = %v, want %v", clean, test.clean)
				}
			})
		}
	}
}

func TestProxyStopTimeoutNeverPersistsReleaseProof(t *testing.T) {
	p, path := cacheShutdownProxy(t)
	p.dealloc.started.Store(1)
	p.dealloc.confirmed.Store(1)
	p.credPool.saveToDisk()
	finish := make(chan struct{})
	joined := make(chan struct{})
	p.wg.Add(1)
	go func() {
		defer close(joined)
		defer p.wg.Done()
		<-p.ctx.Done()
		<-finish
	}()
	p.StopWithTimeout(time.Millisecond)
	before := readReleasedCache(t, path)
	// Release the delayed session even if an assertion below fails.
	close(finish)
	<-joined
	p.wg.Wait()
	if before.ReleasedAt != 0 {
		t.Fatal("timed-out stop wrote a clean release proof")
	}
	if after := readReleasedCache(t, path); after.ReleasedAt != 0 {
		t.Fatal("late session completion wrote a clean release proof after timeout")
	}
}

func TestProxyStopWaitsForThePeriodicSaverBeforeCleanSnapshot(t *testing.T) {
	p, path := cacheShutdownProxy(t)
	p.dealloc.started.Store(1)
	p.dealloc.confirmed.Store(1)
	p.credPool.saveToDisk()
	saverDone := make(chan struct{})
	p.credPool.saverDone = saverDone
	stopped := make(chan struct{})
	go func() {
		p.Stop()
		close(stopped)
	}()
	<-p.ctx.Done()
	select {
	case <-stopped:
		close(saverDone)
		t.Fatal("stop finished before the periodic saver")
	case <-time.After(20 * time.Millisecond):
	}
	// A periodic saver already finishing at cancellation writes its dirty
	// snapshot last. Stop must wait for that save before writing the proof.
	p.credPool.saveToDisk()
	close(saverDone)
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("stop did not finish after the periodic saver")
	}
	if file := readReleasedCache(t, path); file.ReleasedAt == 0 {
		t.Fatal("the periodic saver overwrote the final clean snapshot")
	}
}

func TestLateBackgroundFetchPreservesTheFinalReleaseProof(t *testing.T) {
	p, path := cacheShutdownProxy(t)
	p.dealloc.started.Store(1)
	p.dealloc.confirmed.Store(1)
	fetching := make(chan struct{})
	finishFetch := make(chan struct{})
	finished := make(chan struct{})
	p.credPool.fetch = func(_ bool, _ int) (string, *TURNCreds, error) {
		close(fetching)
		<-finishFetch
		return relayB, &TURNCreds{
			Username:  futureUser("late-background-fetch"),
			Password:  "p",
			Address:   relayB,
			Addresses: []string{relayB},
		}, nil
	}
	t.Cleanup(func() {
		select {
		case <-finishFetch:
		default:
			close(finishFetch)
		}
		<-finished
	})
	go func() {
		defer close(finished)
		p.credPool.tryFill(1, false, 0)
	}()
	<-fetching
	p.Stop()
	before := readReleasedCache(t, path)
	if before.ReleasedAt == 0 {
		t.Fatal("joined shutdown did not write a release proof")
	}
	close(finishFetch)
	<-finished
	after := readReleasedCache(t, path)
	if after.ReleasedAt != before.ReleasedAt {
		t.Fatalf("late credential save changed release proof from %d to %d", before.ReleasedAt, after.ReleasedAt)
	}
	if len(after.Creds) != 2 {
		t.Fatal("late background credential was not preserved in the final cache")
	}
}

func TestNewPoolWaitsForConfirmedReleaseBeforeBootstrap(t *testing.T) {
	releasedAt := time.Now()
	path := writeReleasedCache(t, credCacheModeAnon, releasedAt)
	ctx, cancel := context.WithCancel(context.Background())
	cp := newCredPool(ctx, 2, time.Minute, path, nil)
	t.Cleanup(func() { cancel(); <-cp.saverDone })
	if time.Now().Before(releasedAt.Add(seatCoolFor)) {
		t.Fatal("pool became available before the relay's short release delay")
	}
	cp.mu.Lock()
	ready := cp.freshOnLocked(0, relayA)
	cp.mu.Unlock()
	if !ready {
		t.Fatal("first bootstrap acquire would back off on a confirmed cache")
	}
	if file := readReleasedCache(t, path); file.ReleasedAt != 0 {
		t.Fatal("constructor did not consume the clean proof before bootstrap")
	}
}

func TestNewPoolReleaseDelayStopsWithItsContext(t *testing.T) {
	path := writeReleasedCache(t, credCacheModeAnon, time.Now())
	ctx, cancel := context.WithCancel(context.Background())
	timer := time.AfterFunc(20*time.Millisecond, cancel)
	defer timer.Stop()
	started := time.Now()
	cp := newCredPool(ctx, 2, time.Minute, path, nil)
	t.Cleanup(func() { cancel(); <-cp.saverDone })
	if elapsed := time.Since(started); elapsed >= seatCoolFor/2 {
		t.Fatalf("cancelled constructor waited %s for the release delay", elapsed)
	}
	if ctx.Err() == nil {
		t.Fatal("constructor returned before the test cancelled its context")
	}
}

func TestFutureReleaseProofFallsBackToThePreviousCooldown(t *testing.T) {
	for _, mode := range []string{credCacheModeAnon, credCacheModeCookie} {
		t.Run(mode, func(t *testing.T) {
			path := writeReleasedCache(t, mode, time.Now().Add(time.Minute))
			cp := loadReleasedPool(t, path, mode)
			cp.mu.Lock()
			ready := cp.freshOnLocked(0, relayA)
			cp.mu.Unlock()
			if ready {
				t.Fatal("a future release timestamp bypassed the previous cooldown")
			}
			if file := readReleasedCache(t, path); file.ReleasedAt != 0 {
				t.Fatal("a future release proof remained on disk")
			}
		})
	}
}

func TestUnconsumedReleaseProofBlocksOtherwiseFreshCredentials(t *testing.T) {
	for _, mode := range []string{credCacheModeAnon, credCacheModeCookie} {
		t.Run(mode, func(t *testing.T) {
			path := writeReleasedCache(t, mode, time.Now().Add(-3*time.Second))
			file := readReleasedCache(t, path)
			old := time.Now().Add(-2 * credSaturationCooldown).Unix()
			file.Creds[0].LastUsedAt = old
			file.Creds[0].UsedOn = map[string]int64{relayA: old}
			if mode == credCacheModeCookie {
				file.RelayUsedAt = map[string]int64{relayA: old}
			}
			data, err := json.Marshal(file)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			blocked := path + ".tmp"
			if err := os.Mkdir(blocked, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(blocked, "keep"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			cp := loadReleasedPool(t, path, mode)
			cp.mu.Lock()
			fresh := cp.freshOnLocked(0, relayA)
			cp.mu.Unlock()
			if !fresh {
				t.Fatal("fixture must be reusable by the legacy cooldown rule")
			}
			if _, _, _, err := cp.get(0, false); err == nil {
				t.Fatal("an unconsumed proof allowed a new allocation before durable invalidation")
			}
			cp.mu.Lock()
			active := cp.pool[0].active
			cp.mu.Unlock()
			if active != 0 {
				t.Fatal("failed invalidation still handed out a credential lease")
			}
			if err := os.RemoveAll(blocked); err != nil {
				t.Fatal(err)
			}
			addr, creds, slot, err := cp.get(0, false)
			if err != nil {
				t.Fatalf("cached acquire did not recover after restoring cache writes: %v", err)
			}
			if addr != relayA || slot != 0 || creds.Username != file.Creds[0].Username {
				t.Fatal("recovered acquire did not reuse the cached credential")
			}
			if disk := readReleasedCache(t, path); disk.ReleasedAt != 0 {
				t.Fatal("recovered acquire did not durably invalidate the previous proof")
			}
			cp.release(slot, creds, addr)
		})
	}
}
