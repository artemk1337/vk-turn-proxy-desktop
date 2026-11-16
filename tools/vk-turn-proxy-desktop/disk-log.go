// SPDX-License-Identifier: MIT

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const diskLogQueueLines = 1024
const diskLogBatchLines = 64
const diskLogWait = 2 * time.Second

type diskLogEntry struct {
	at   time.Time
	line string
}

// The queue mutex only protects memory. The worker owns all filesystem access.
type diskLog struct {
	root    string
	day     string
	mu      sync.Mutex
	entries []diskLogEntry
	bytes   int
	dropped int
	closed  bool
	wake    chan struct{}
	flushes chan chan struct{}
	stop    chan struct{}
	done    chan struct{}
}

func (l *diskLog) start(write func([]diskLogEntry) error, report func(string)) {
	l.wake = make(chan struct{}, 1)
	l.flushes = make(chan chan struct{}, 1)
	l.stop = make(chan struct{})
	l.done = make(chan struct{})
	go func() {
		defer close(l.done)
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		failed, overflowReported := false, false
		flush := func() {
			l.mu.Lock()
			entries, dropped := l.entries, l.dropped
			l.entries, l.bytes, l.dropped = nil, 0, 0
			l.mu.Unlock()
			if dropped > 0 {
				message := fmt.Sprintf("Connection log buffer overflow: %d lines dropped.", dropped)
				entries = append(entries, diskLogEntry{at: time.Now(), line: message})
				if !overflowReported {
					report(message)
					overflowReported = true
				}
			}
			if len(entries) == 0 {
				return
			}
			if err := write(entries); err != nil && !failed {
				failed = true
				report("Connection logs could not be saved to disk.")
			}
		}
		for {
			select {
			case <-ticker.C:
				flush()
			case <-l.wake:
				flush()
			case done := <-l.flushes:
				flush()
				close(done)
			case <-l.stop:
				flush()
				return
			}
		}
	}()
}

func (l *diskLog) enqueue(now time.Time, line string) {
	if l.root == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return
	}
	if len(l.entries) >= diskLogQueueLines || len(line)+1 > defaults.LogMaxBytes-l.bytes {
		l.dropped++
		return
	}
	l.entries = append(l.entries, diskLogEntry{at: now, line: line})
	l.bytes += len(line) + 1
	if len(l.entries) >= diskLogBatchLines {
		select {
		case l.wake <- struct{}{}:
		default:
		}
	}
}

// Waiting is bounded so a stalled filesystem cannot hold up disconnect or exit.
func (l *diskLog) flush() {
	if l.root == "" {
		return
	}
	timer := time.NewTimer(diskLogWait)
	defer timer.Stop()
	done := make(chan struct{})
	select {
	case l.flushes <- done:
	case <-l.done:
		return
	case <-timer.C:
		return
	}
	select {
	case <-done:
	case <-l.done:
	case <-timer.C:
	}
}

func (l *diskLog) close() {
	if l.root == "" {
		return
	}
	l.mu.Lock()
	if !l.closed {
		l.closed = true
		close(l.stop)
	}
	l.mu.Unlock()
	timer := time.NewTimer(diskLogWait)
	defer timer.Stop()
	select {
	case <-l.done:
	case <-timer.C:
	}
}

func (l *diskLog) prepare(now time.Time) error {
	if err := secureDirectory(l.root); err != nil {
		return err
	}
	entries, err := os.ReadDir(l.root)
	if err != nil {
		return err
	}
	cutoff := now.AddDate(0, 0, 1-defaults.LogRetentionDays).Format("2006-01-02")
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".log") {
			continue
		}
		day := strings.TrimSuffix(name, ".log")
		if _, err := time.Parse("2006-01-02", day); err != nil {
			continue
		}
		if day < cutoff {
			if err := os.Remove(filepath.Join(l.root, name)); err != nil {
				return err
			}
		}
	}
	l.day = now.Format("2006-01-02")
	return nil
}

func (l *diskLog) writeBatch(entries []diskLogEntry) error {
	for len(entries) > 0 {
		day := entries[0].at.Format("2006-01-02")
		var data strings.Builder
		count := 0
		for count < len(entries) && entries[count].at.Format("2006-01-02") == day {
			entry := entries[count]
			data.WriteString(entry.at.Format(time.RFC3339Nano))
			data.WriteByte(' ')
			data.WriteString(entry.line)
			data.WriteByte('\n')
			count++
		}
		if err := secureDirectory(l.root); err != nil {
			return err
		}
		if day != l.day {
			if err := l.prepare(entries[0].at); err != nil {
				return err
			}
			l.day = day
		}
		path := filepath.Join(l.root, day+".log")
		if info, err := os.Lstat(path); err == nil {
			if !info.Mode().IsRegular() {
				return errors.New("connection log must be a regular file")
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		if err = file.Chmod(0600); err == nil {
			_, err = file.WriteString(data.String())
		}
		closeErr := file.Close()
		if err = errors.Join(err, closeErr); err != nil {
			return err
		}
		entries = entries[count:]
	}
	return nil
}
