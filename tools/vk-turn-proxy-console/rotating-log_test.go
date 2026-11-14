package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func testRotatingLog(t *testing.T, limit int64) (*rotatingLog, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "client.log")
	w, err := newRotatingLog(path, func(string) string { return "" }, limit)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close() })
	return w, path
}

func readLog(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestRotatingLogKeepsOneBoundedBackup(t *testing.T) {
	w, path := testRotatingLog(t, 64)
	var all strings.Builder
	for i := 0; i < 50; i++ {
		line := fmt.Sprintf("entry %03d\n", i)
		all.WriteString(line)
		if n, err := io.WriteString(w, line); err != nil || n != len(line) {
			t.Fatalf("write returned %d, %v", n, err)
		}
	}
	combined := readLog(t, path+".1") + readLog(t, path)
	if !strings.HasSuffix(all.String(), combined) || !strings.HasSuffix(combined, "entry 049\n") {
		t.Fatal("rotation did not retain the most recent complete entries")
	}
	for _, name := range []string{path, path + ".1"} {
		info, err := os.Stat(name)
		if err != nil || info.Size() > 64 {
			t.Fatal("a disk log exceeded its limit")
		}
	}
	if _, err := os.Stat(path + ".2"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("rotation created more than one backup")
	}
}

func TestRotatingLogBoundsAnOversizedWrite(t *testing.T) {
	w, path := testRotatingLog(t, 64)
	data := []byte(strings.Repeat("0123456789", 30))
	if n, err := w.Write(data); err != nil || n != len(data) {
		t.Fatalf("oversized write returned %d, %v", n, err)
	}
	combined := readLog(t, path+".1") + readLog(t, path)
	if len(combined) > 128 || !bytes.HasSuffix(data, []byte(combined)) {
		t.Fatal("oversized entry was not retained as a bounded recent tail")
	}
}

func TestRotatingLogAppendsAcrossReopens(t *testing.T) {
	w, path := testRotatingLog(t, 64)
	if _, err := io.WriteString(w, "first\n"); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := newRotatingLog(path, func(string) string { return "" }, 64)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if _, err := io.WriteString(second, "second\n"); err != nil {
		t.Fatal(err)
	}
	if readLog(t, path) != "first\nsecond\n" {
		t.Fatal("opening the rotating log discarded earlier entries")
	}
}

func TestRotatingLogTrimsLargeFilesFromEarlierVersions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "client.log")
	for _, name := range []string{path, path + ".1"} {
		if err := os.WriteFile(name, []byte(strings.Repeat("old ", 100)+"recent"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	w, err := newRotatingLog(path, func(string) string { return "" }, 64)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	for _, name := range []string{path, path + ".1"} {
		contents := readLog(t, name)
		if len(contents) != 64 || !strings.HasSuffix(contents, "recent") {
			t.Fatal("legacy log was not reduced to its recent bounded tail")
		}
	}
}

func TestRotatingLogRefusesASecondOwnerBeforeTrimming(t *testing.T) {
	w, path := testRotatingLog(t, 64)
	if _, err := io.WriteString(w, "an active writer\n"); err != nil {
		t.Fatal(err)
	}
	before := readLog(t, path)
	if second, err := newRotatingLog(path, func(string) string { return "" }, 4); err == nil {
		second.Close()
		t.Fatal("a second process could acquire the same log")
	}
	if readLog(t, path) != before {
		t.Fatal("the rejected writer modified an active log")
	}
}

func TestRotatingLogReportsRotationFailureAndRecovers(t *testing.T) {
	w, path := testRotatingLog(t, 8)
	var stderr bytes.Buffer
	w.stderr = &stderr
	if _, err := io.WriteString(w, "12345678"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path+".1", 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path+".1", "blocked"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if n, err := io.WriteString(w, "next"); n != 0 || err == nil {
			t.Fatal("failed rotation reported a successful write")
		}
	}
	if strings.Count(stderr.String(), "stderr logging continues") != 1 {
		t.Fatal("rotation errors were silent or flooded stderr")
	}
	if readLog(t, path) != "12345678" {
		t.Fatal("failed rotation grew or discarded the active log")
	}
	if err := os.RemoveAll(path + ".1"); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(w, "next"); err != nil {
		t.Fatal(err)
	}
	if readLog(t, path) != "next" || readLog(t, path+".1") != "12345678" {
		t.Fatal("file logging did not recover after a rotation failure")
	}
}

func TestRotatingLogCloseReleasesItsLockAndRejectsWrites(t *testing.T) {
	w, path := testRotatingLog(t, 64)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if n, err := w.Write([]byte("late")); n != 0 || !errors.Is(err, os.ErrClosed) {
		t.Fatal("closed log accepted a write")
	}
	lock, err := takePidFile(path + ".lock")
	if err != nil {
		t.Fatal("closing the writer kept its process lock")
	}
	lock.release()
}

func TestRotatingLogSerializesConcurrentEntries(t *testing.T) {
	w, path := testRotatingLog(t, 128)
	var workers sync.WaitGroup
	for i := 0; i < 100; i++ {
		workers.Add(1)
		go func(index int) {
			defer workers.Done()
			if _, err := fmt.Fprintf(w, "entry %03d\n", index); err != nil {
				t.Error(err)
			}
		}(i)
	}
	workers.Wait()
	for _, name := range []string{path, path + ".1"} {
		contents := readLog(t, name)
		if len(contents) > 128 {
			t.Fatal("concurrent writes exceeded the size limit")
		}
		for _, line := range strings.Split(strings.TrimSuffix(contents, "\n"), "\n") {
			if len(line) != 9 || !strings.HasPrefix(line, "entry ") {
				t.Fatal("concurrent entries were interleaved or truncated")
			}
		}
	}
}

func TestRotatingLogPreservesFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows uses the application directory ACL")
	}
	path := filepath.Join(t.TempDir(), "client.log")
	if err := os.WriteFile(path, []byte("12345678"), 0640); err != nil {
		t.Fatal(err)
	}
	w, err := newRotatingLog(path, func(string) string { return "" }, 8)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if _, err := io.WriteString(w, "next"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{path, path + ".1"} {
		info, err := os.Stat(name)
		if err != nil || info.Mode().Perm() != 0640 {
			t.Fatal("rotation changed the existing log permissions")
		}
	}
}

func TestRotatingLogRejectsInvalidLimitsAndReleasesFailedOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "directory")
	if _, err := newRotatingLog(path, os.Getenv, 0); err == nil {
		t.Fatal("invalid log limit was accepted")
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := newRotatingLog(path, os.Getenv, 64); err == nil {
		t.Fatal("a directory was accepted as the rotating log")
	}
	lock, err := takePidFile(path + ".lock")
	if err != nil {
		t.Fatal("failed log initialization kept its lock")
	}
	lock.release()
}
