// SPDX-License-Identifier: MIT

package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
)

const diskLogLimit = 5 << 20

type rotatingLog struct {
	mu        sync.Mutex
	path      string
	getenv    func(string) string
	limit     int64
	file      *os.File
	size      int64
	closed    bool
	lastError string
	stderr    io.Writer
	lock      *pidFile
}

func openRotatingLog(path string, getenv func(string) string) (*rotatingLog, error) {
	return newRotatingLog(path, getenv, diskLogLimit)
}

func newRotatingLog(path string, getenv func(string) string, limit int64) (*rotatingLog, error) {
	if limit <= 0 {
		return nil, errors.New("log size limit must be positive")
	}
	lock, err := takePidFile(path + ".lock")
	if err != nil {
		return nil, fmt.Errorf("lock log file: %w", err)
	}
	for _, name := range []string{path, path + ".1"} {
		if err := trimLogTail(name, limit); err != nil {
			lock.release()
			return nil, err
		}
	}
	w := &rotatingLog{path: path, getenv: getenv, limit: limit, stderr: os.Stderr, lock: lock}
	if err := w.open(); err != nil {
		lock.release()
		return nil, err
	}
	return w, nil
}

// Write retains complete ordinary entries and splits entries larger than a file.
func (w *rotatingLog) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return 0, os.ErrClosed
	}
	if w.file == nil {
		if err := w.open(); err != nil {
			return 0, w.failure(err)
		}
	}
	written := 0
	for len(p) > 0 {
		if w.size > 0 && int64(len(p)) > w.limit-w.size {
			if err := w.rotate(); err != nil {
				return written, w.failure(err)
			}
		}
		part := p[:min(int64(len(p)), w.limit)]
		n, err := w.file.Write(part)
		w.size += int64(n)
		written += n
		p = p[n:]
		if err == nil && n < len(part) {
			err = io.ErrShortWrite
		}
		if err != nil {
			return written, w.failure(err)
		}
	}
	w.lastError = ""
	return written, nil
}

func (w *rotatingLog) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	defer w.lock.release()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

func (w *rotatingLog) open() error {
	f, err := openLog(w.path, w.getenv)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	if !info.Mode().IsRegular() {
		f.Close()
		return errors.New("rotating log requires a regular file")
	}
	w.file, w.size = f, info.Size()
	return nil
}

func (w *rotatingLog) rotate() error {
	info, err := w.file.Stat()
	if err != nil {
		return err
	}
	err = w.file.Close()
	w.file = nil
	if err != nil {
		return err
	}
	backup := w.path + ".1"
	if err := os.Remove(backup); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(w.path, backup); err != nil {
		return err
	}
	if err := w.open(); err != nil {
		return err
	}
	if err := w.file.Chmod(info.Mode().Perm()); err != nil {
		return err
	}
	return restoreLogOwner(w.file, info)
}

func (w *rotatingLog) failure(err error) error {
	if message := err.Error(); message != w.lastError {
		fmt.Fprintf(w.stderr, "-log: file logging failed (%v), stderr logging continues\n", err)
		w.lastError = message
	}
	return err
}

// Older versions could leave a large file. Read only its retained tail.
func trimLogTail(path string, limit int64) error {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() <= limit {
		return nil
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	tail := make([]byte, limit)
	if _, err := f.ReadAt(tail, info.Size()-limit); err != nil {
		return err
	}
	if err := f.Truncate(0); err != nil {
		return err
	}
	n, err := f.WriteAt(tail, 0)
	if err == nil && n < len(tail) {
		err = io.ErrShortWrite
	}
	return err
}
