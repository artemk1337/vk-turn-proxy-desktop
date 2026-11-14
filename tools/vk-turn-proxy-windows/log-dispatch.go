// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"errors"
	"io"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	logPendingBytes = 64 << 10
	logPendingLines = 256
	logLineBytes    = 8 << 10
	logFlushPeriod  = 100 * time.Millisecond
)

type logBatch struct {
	text    string
	phase   string
	dropped uint64
}

// The pending ring bounds work even while the Windows message loop is busy.
// Only one flush callback can be queued, regardless of the backend's log rate.
type logDispatcher struct {
	mu        sync.Mutex
	lines     [logPendingLines]string
	head      int
	count     int
	bytes     int
	dropped   uint64
	phase     string
	phaseRank int
	queued    bool
	finished  bool
	stopped   bool
	stop      chan struct{}
	schedule  func(func())
	deliver   func(logBatch)
	onFinish  func()
}

func newLogDispatcher(schedule func(func()), deliver func(logBatch)) *logDispatcher {
	d := &logDispatcher{stop: make(chan struct{}), schedule: schedule, deliver: deliver}
	go func() {
		ticker := time.NewTicker(logFlushPeriod)
		defer ticker.Stop()
		for {
			select {
			case <-d.stop:
				return
			case <-ticker.C:
				d.requestFlush()
			}
		}
	}()
	return d
}

func (d *logDispatcher) add(line string) {
	line = boundedLogLine(line)
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.stopped || d.finished {
		return
	}
	if rank, phase := logPhase(line); rank > d.phaseRank {
		d.phaseRank, d.phase = rank, phase
	}
	for d.count == logPendingLines || d.bytes+len(line)+1 > logPendingBytes {
		d.bytes -= len(d.lines[d.head]) + 1
		d.lines[d.head] = ""
		d.head = (d.head + 1) % logPendingLines
		d.count--
		d.dropped++
	}
	d.lines[(d.head+d.count)%logPendingLines] = line
	d.count++
	d.bytes += len(line) + 1
}

func logPhase(line string) (int, string) {
	switch {
	case strings.Contains(line, "DEFAULT ROUTE → the tunnel"):
		return 3, "DEFAULT ROUTE → the tunnel"
	case strings.Contains(line, "proxy: first connection up"), strings.Contains(line, "tunnel:"):
		return 2, "proxy: first connection up"
	case strings.Contains(line, "TURN relay allocated:"):
		return 1, "TURN relay allocated:"
	}
	return 0, ""
}

func (d *logDispatcher) requestFlush() {
	d.mu.Lock()
	if d.stopped || d.queued || (d.count == 0 && !d.finished) {
		d.mu.Unlock()
		return
	}
	d.queued = true
	schedule := d.schedule
	d.mu.Unlock()
	schedule(d.flush)
}

func (d *logDispatcher) flush() {
	d.mu.Lock()
	if d.stopped {
		d.mu.Unlock()
		return
	}
	lines := make([]string, d.count)
	for i := range lines {
		index := (d.head + i) % logPendingLines
		lines[i], d.lines[index] = d.lines[index], ""
	}
	batch := logBatch{text: strings.Join(lines, "\n"), phase: d.phase, dropped: d.dropped}
	d.head, d.count, d.bytes, d.dropped, d.phase = 0, 0, 0, 0, ""
	deliver := d.deliver
	d.mu.Unlock()
	if len(lines) > 0 || batch.dropped > 0 || batch.phase != "" {
		deliver(batch)
	}
	d.mu.Lock()
	if d.stopped {
		d.mu.Unlock()
		return
	}
	d.queued = false
	var finish func()
	if d.finished && d.count == 0 {
		finish = d.onFinish
		d.closeLocked()
	}
	d.mu.Unlock()
	if finish != nil {
		finish()
	}
}

// Finish runs the process-exit update after every retained log and phase update.
func (d *logDispatcher) finish(callback func()) {
	d.mu.Lock()
	if d.stopped {
		d.mu.Unlock()
		return
	}
	d.finished, d.onFinish = true, callback
	d.mu.Unlock()
	d.requestFlush()
}

func (d *logDispatcher) close() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.stopped {
		d.closeLocked()
	}
}

func (d *logDispatcher) closeLocked() {
	d.stopped = true
	d.lines = [logPendingLines]string{}
	d.count, d.bytes = 0, 0
	d.schedule, d.deliver, d.onFinish = nil, nil, nil
	close(d.stop)
}

func boundedLogLine(line string) string {
	const suffix = " [log line truncated]"
	if len(line) <= logLineBytes {
		return line
	}
	end := logLineBytes - len(suffix)
	for end > 0 && !utf8.RuneStart(line[end]) {
		end--
	}
	return strings.Clone(line[:end]) + suffix
}

// ReadSlice drains a long line without retaining its discarded tail or stopping
// pipe consumption. This prevents a noisy backend from blocking its own logger.
func readLogLine(reader *bufio.Reader) (string, error) {
	var prefix []byte
	truncated := false
	for {
		chunk, err := reader.ReadSlice('\n')
		remaining := logLineBytes - len(prefix)
		if len(chunk) > remaining {
			prefix = append(prefix, chunk[:remaining]...)
			truncated = true
		} else {
			prefix = append(prefix, chunk...)
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		line := strings.TrimSuffix(strings.TrimSuffix(string(prefix), "\n"), "\r")
		if truncated {
			line = boundedLogLine(line + " ")
		}
		return line, err
	}
}

func readBackendLogs(reader io.Reader, add func(string)) {
	buffer := bufio.NewReaderSize(reader, 16<<10)
	for {
		line, err := readLogLine(buffer)
		if line != "" || err == nil {
			add(line)
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				add("Log reader: " + err.Error())
			}
			return
		}
	}
}
