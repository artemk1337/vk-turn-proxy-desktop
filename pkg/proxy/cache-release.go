package proxy

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
)

// saveStoppedCache waits for the periodic saver before persisting the final pool.
// Cached credentials are retried immediately, including after interrupted sessions.
func (p *Proxy) saveStoppedCache() {
	cp := p.credPool
	if cp.cachePath == "" {
		return
	}
	if cp.saverDone != nil {
		<-cp.saverDone
	}
	cp.saveToDisk()
}

// writeCache requires saveMu. Sync and atomically replace the private cache.
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
