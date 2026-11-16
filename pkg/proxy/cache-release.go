package proxy

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"
)

// saveStoppedCache runs only after all sessions have joined. Failed or
// unanswered Allocate attempts cannot certify that the relay holds no seats.
func (p *Proxy) saveStoppedCache() {
	cp := p.credPool
	if cp.cachePath == "" {
		return
	}
	if cp.saverDone != nil {
		<-cp.saverDone
	}
	if p.dealloc.started.Load() == p.dealloc.confirmed.Load() &&
		p.dealloc.unconfirmed.Load() == 0 && p.dealloc.legsLeft.Load() == 0 {
		cp.saveCache(time.Now())
		return
	}
	cp.saveToDisk()
}

// consumeReleasedCache durably removes a shutdown proof before any new
// allocation can use it. A crash in the next session must load a dirty cache.
func (cp *credPool) consumeReleasedCache(f credCacheFile) bool {
	now := time.Now()
	if f.ReleasedAt <= 0 {
		return false
	}
	cp.saveMu.Lock()
	defer cp.saveMu.Unlock()
	if err := cp.writeCache(dirtyCredCache(f, now)); err != nil {
		cp.pendingRelease = true
		log.Printf("credpool: cannot invalidate release confirmation: %v — allocations are paused until the cache can be written", err)
		return false
	}
	cp.cacheRestarted = true
	if time.Unix(0, f.ReleasedAt).After(now) {
		return false
	}
	log.Printf("credpool: previous shutdown released every allocation — reusing cached credentials after the relay's short release delay")
	return true
}

// ensureCacheDirty retries a failed write-ahead barrier. Even a legacy-ready
// credential must not allocate while a stale clean proof remains on disk.
func (cp *credPool) ensureCacheDirty() bool {
	cp.saveMu.Lock()
	pending := cp.pendingRelease
	cp.saveMu.Unlock()
	if !pending {
		return true
	}
	cp.saveToDisk()
	cp.saveMu.Lock()
	defer cp.saveMu.Unlock()
	return !cp.pendingRelease
}

// dirtyCredCache is conservative about every credential admitted by a clean
// restart, including reserves not yet handed out when a snapshot is taken.
func dirtyCredCache(f credCacheFile, now time.Time) credCacheFile {
	f.ReleasedAt = 0
	f.SavedAt = now.Unix()
	f.Creds = append([]credCacheEntry(nil), f.Creds...)
	relays := make(map[string]int64, len(f.RelayUsedAt)+len(f.Creds))
	for relay := range f.RelayUsedAt {
		relays[relay] = now.Unix()
	}
	for i := range f.Creds {
		e := &f.Creds[i]
		used := map[string]int64{"*": now.Unix()}
		for relay := range e.UsedOn {
			used[relay] = now.Unix()
		}
		e.LastUsedAt, e.UsedOn = now.Unix(), used
		if e.Address != "" {
			relays[e.Address] = now.Unix()
		}
	}
	if f.Mode == credCacheModeCookie {
		f.RelayUsedAt = relays
	}
	return f
}

// writeCache requires saveMu. Sync the contents before the atomic rename so
// the dirty barrier and the final release proof survive a process crash.
func (cp *credPool) writeCache(f credCacheFile) error {
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmpPath := cp.cachePath + ".tmp"
	_ = os.Remove(tmpPath)
	file, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer os.Remove(tmpPath)
	_, writeErr := file.Write(data)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Rename(tmpPath, cp.cachePath); err != nil {
		return fmt.Errorf("rename credential cache: %w", err)
	}
	// Some platforms/filesystems do not support syncing a directory.
	if dir, err := os.Open(filepath.Dir(cp.cachePath)); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	log.Printf("credpool: saved %d slots to disk", len(f.Creds))
	return nil
}
