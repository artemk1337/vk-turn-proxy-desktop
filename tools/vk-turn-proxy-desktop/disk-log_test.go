// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDiskLogRetainsSevenCalendarDaysAndAppends(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 11, 16, 23, 59, 0, 0, time.FixedZone("test", 3*60*60))
	for _, name := range []string{"2026-11-09.log", "2026-11-10.log", "2026-11-16.log", "notes.log", "2026-11-01.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("previous\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	l := diskLog{root: root}
	if err := l.prepare(now); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "2026-11-09.log")); !os.IsNotExist(err) {
		t.Fatal("expired log retained")
	}
	for _, name := range []string{"2026-11-10.log", "notes.log", "2026-11-01.txt"} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.writeBatch([]diskLogEntry{{at: now, line: "deallocate confirmed"}}); err != nil {
		t.Fatal(err)
	}
	restarted := diskLog{root: root}
	if err := restarted.writeBatch([]diskLogEntry{{at: now, line: "next session"}}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "2026-11-16.log")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "previous\n") || !strings.Contains(string(data), "deallocate confirmed\n") || !strings.Contains(string(data), "next session\n") {
		t.Fatalf("lost earlier log lines: %s", data)
	}
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(path)
		if info.Mode().Perm() != 0600 {
			t.Fatal("log file is not private")
		}
		info, _ = os.Stat(root)
		if info.Mode().Perm() != 0700 {
			t.Fatal("log directory is not private")
		}
	}
	tomorrow := now.Add(2 * time.Minute)
	if err := l.writeBatch([]diskLogEntry{{at: tomorrow, line: "new day"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "2026-11-10.log")); !os.IsNotExist(err) {
		t.Fatal("date change did not expire old log")
	}
	if _, err := os.Stat(filepath.Join(root, "2026-11-17.log")); err != nil {
		t.Fatal(err)
	}
}

func TestAppDiskLogsSurviveCancellationAndClear(t *testing.T) {
	a := testApp(t)
	a.diskLogs = diskLog{root: filepath.Join(a.root, "logs")}
	a.diskLogs.start(a.diskLogs.writeBatch, a.reportDiskLog)
	defer a.diskLogs.close()
	a.connection = "connected"
	a.appendLog("desktop-traffic 123 456")
	a.appendLog("session started")
	if _, err := a.ClearLogs(); err != nil {
		t.Fatal(err)
	}
	a.cancel()
	a.appendLog("deallocate confirmed")
	a.diskLogs.flush()
	path := filepath.Join(a.diskLogs.root, time.Now().Format("2006-01-02")+".log")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "desktop-traffic") {
		t.Fatal("traffic protocol persisted")
	}
	if !strings.Contains(string(data), "session started") || !strings.Contains(string(data), "deallocate confirmed") {
		t.Fatal("clear or cancellation lost connection history")
	}
}

func TestAppPrunesDiskLogsOnStartup(t *testing.T) {
	root := t.TempDir()
	logs := filepath.Join(root, "logs")
	if err := os.Mkdir(logs, 0700); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(logs, time.Now().AddDate(0, 0, -7).Format("2006-01-02")+".log")
	if err := os.WriteFile(old, nil, 0600); err != nil {
		t.Fatal(err)
	}
	a, err := newApp(root, "", func() {})
	if err != nil {
		t.Fatal(err)
	}
	defer a.shutdown()
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("startup retained expired log")
	}
}

func TestDiskLoggingFailureIsReportedOnce(t *testing.T) {
	a := testApp(t)
	path := filepath.Join(a.root, "file")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	a.diskLogs = diskLog{root: path}
	a.diskLogs.start(a.diskLogs.writeBatch, a.reportDiskLog)
	defer a.diskLogs.close()
	a.appendLog("visible line")
	a.diskLogs.flush()
	if !strings.Contains(a.lastError, "logs could not be saved") {
		t.Fatal("disk failure was hidden")
	}
	a.lastError = ""
	a.appendLog("another visible line")
	a.diskLogs.flush()
	if a.lastError != "" || len(a.logs.Lines) != 2 {
		t.Fatal("disk failure repeated or interrupted visible logging")
	}
}

func TestDiskLogQueueIsNonblockingAndReportsOverflow(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var once, releaseOnce sync.Once
	var batches [][]diskLogEntry
	var messages []string
	l := diskLog{root: t.TempDir()}
	defer func() { releaseOnce.Do(func() { close(release) }); l.close() }()
	l.start(func(entries []diskLogEntry) error {
		once.Do(func() { close(started); <-release })
		batches = append(batches, entries)
		return nil
	}, func(message string) { messages = append(messages, message) })
	for i := 0; i < diskLogBatchLines; i++ {
		l.enqueue(time.Now(), "first batch")
	}
	<-started
	finished := make(chan struct{})
	go func() {
		for i := 0; i < diskLogQueueLines+10; i++ {
			l.enqueue(time.Now(), "queued line")
		}
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("enqueue blocked behind disk IO")
	}
	releaseOnce.Do(func() { close(release) })
	l.close()
	if len(batches) < 2 || !strings.Contains(batches[len(batches)-1][len(batches[len(batches)-1])-1].line, "10 lines dropped") {
		t.Fatal("missing overflow notice")
	}
	if len(messages) != 1 || !strings.Contains(messages[0], "overflow") {
		t.Fatal("overflow was not reported once")
	}
}

func TestDiskLogCloseDrainsAndDeletedFileIsRecreated(t *testing.T) {
	root := t.TempDir()
	l := diskLog{root: root}
	l.start(l.writeBatch, func(message string) { t.Error(message) })
	l.enqueue(time.Now(), "before deletion")
	l.flush()
	paths, err := filepath.Glob(filepath.Join(root, "*.log"))
	if err != nil || len(paths) != 1 {
		t.Fatal("missing initial log")
	}
	if err := os.Remove(paths[0]); err != nil {
		t.Fatal(err)
	}
	l.enqueue(time.Now(), "after deletion")
	l.flush()
	data, err := os.ReadFile(paths[0])
	if err != nil || !strings.Contains(string(data), "after deletion") {
		t.Fatal("deleted file was not recreated")
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	l.enqueue(time.Now(), "after directory deletion")
	l.close()
	l.close()
	data, err = os.ReadFile(paths[0])
	if err != nil || !strings.Contains(string(data), "after directory deletion") {
		t.Fatal("deleted directory was not recreated on drain")
	}
}

func TestDiskLogFlushAndCloseHaveBoundedWaits(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	l := diskLog{root: t.TempDir()}
	l.start(func([]diskLogEntry) error { close(started); <-release; return nil }, func(string) {})
	defer func() { releaseOnce.Do(func() { close(release) }); l.close() }()
	for i := 0; i < diskLogBatchLines; i++ {
		l.enqueue(time.Now(), "blocked batch")
	}
	<-started
	flushed, closed := make(chan struct{}), make(chan struct{})
	go func() { l.flush(); close(flushed) }()
	go func() { l.close(); close(closed) }()
	timeout := time.NewTimer(diskLogWait + time.Second)
	defer timeout.Stop()
	for _, done := range []chan struct{}{flushed, closed} {
		select {
		case <-done:
		case <-timeout.C:
			t.Fatal("stalled disk blocked flush or close past its wait bound")
		}
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case <-l.done:
	case <-time.After(time.Second):
		t.Fatal("worker did not finish after disk recovered")
	}
}
