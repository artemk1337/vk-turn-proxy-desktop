// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

type queuedLogCallbacks struct {
	mu        sync.Mutex
	callbacks []func()
	maximum   int
}

func (q *queuedLogCallbacks) schedule(callback func()) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.callbacks = append(q.callbacks, callback)
	q.maximum = max(q.maximum, len(q.callbacks))
}

func (q *queuedLogCallbacks) run() bool {
	q.mu.Lock()
	if len(q.callbacks) == 0 {
		q.mu.Unlock()
		return false
	}
	callback := q.callbacks[0]
	q.callbacks = q.callbacks[1:]
	q.mu.Unlock()
	callback()
	return true
}

func TestLogFloodBoundsPendingMemoryAndWindowsCallbacks(t *testing.T) {
	var callbacks queuedLogCallbacks
	var received logBatch
	finished := false
	dispatcher := newLogDispatcher(callbacks.schedule, func(batch logBatch) { received = batch })
	defer dispatcher.close()
	dispatcher.add("DEFAULT ROUTE → the tunnel")
	dispatcher.requestFlush()
	for i := 0; i < 1_000_000; i++ {
		dispatcher.add("stats: backend is still writing while the window is busy")
		if i%1000 == 0 {
			dispatcher.requestFlush()
		}
	}
	dispatcher.mu.Lock()
	if dispatcher.bytes > logPendingBytes || dispatcher.count > logPendingLines {
		t.Fatal("pending log work exceeded its bounds")
	}
	dispatcher.mu.Unlock()
	dispatcher.finish(func() { finished = true })
	if !callbacks.run() || !finished {
		t.Fatal("the final callback did not drain and finish the connection")
	}
	if callbacks.maximum != 1 {
		t.Fatalf("blocked Windows loop accumulated %d callbacks", callbacks.maximum)
	}
	if received.phase != "DEFAULT ROUTE → the tunnel" || received.dropped == 0 {
		t.Fatal("log overflow lost connection readiness or its drop notice")
	}
	if len(received.text) > logPendingBytes {
		t.Fatal("delivered batch exceeded the pending byte bound")
	}
}

func TestLogBatchRetainsNewestLinesInOrder(t *testing.T) {
	var callbacks queuedLogCallbacks
	var received logBatch
	dispatcher := newLogDispatcher(callbacks.schedule, func(batch logBatch) { received = batch })
	defer dispatcher.close()
	for i := 0; i < logPendingLines+17; i++ {
		dispatcher.add(fmt.Sprintf("line %d", i))
	}
	dispatcher.requestFlush()
	callbacks.run()
	lines := strings.Split(received.text, "\n")
	if len(lines) != logPendingLines || lines[0] != "line 17" || lines[len(lines)-1] != fmt.Sprintf("line %d", logPendingLines+16) || received.dropped != 17 {
		t.Fatal("bounded queue changed order or dropped the newest lines")
	}
}

func TestLogPhaseProgressSurvivesLaterNoiseAndDoesNotRegress(t *testing.T) {
	var callbacks queuedLogCallbacks
	var phases []string
	dispatcher := newLogDispatcher(callbacks.schedule, func(batch logBatch) { phases = append(phases, batch.phase) })
	defer dispatcher.close()
	dispatcher.add("TURN credential request failed")
	dispatcher.add("TURN relay allocated: test relay")
	dispatcher.add("proxy: first connection up")
	dispatcher.add("DEFAULT ROUTE → the tunnel")
	dispatcher.requestFlush()
	callbacks.run()
	dispatcher.add("tunnel: old diagnostic arrived after routing readiness")
	dispatcher.requestFlush()
	callbacks.run()
	if len(phases) != 2 || phases[0] != "DEFAULT ROUTE → the tunnel" || phases[1] != "" {
		t.Fatalf("connection progress regressed: %q", phases)
	}
}

func TestLogFinishWaitsForRetainedBatchAndReleasesReferences(t *testing.T) {
	var callbacks queuedLogCallbacks
	var events []string
	dispatcher := newLogDispatcher(callbacks.schedule, func(batch logBatch) { events = append(events, batch.text) })
	defer dispatcher.close()
	dispatcher.add("last backend line")
	dispatcher.finish(func() { events = append(events, "finished") })
	callbacks.run()
	if strings.Join(events, "|") != "last backend line|finished" {
		t.Fatal("process completion overtook pending log delivery")
	}
	dispatcher.mu.Lock()
	defer dispatcher.mu.Unlock()
	if !dispatcher.stopped || dispatcher.schedule != nil || dispatcher.deliver != nil || dispatcher.onFinish != nil || dispatcher.count != 0 {
		t.Fatal("finished session retained callbacks or log data")
	}
}

func TestClosingLogDispatcherInvalidatesItsQueuedCallback(t *testing.T) {
	var callbacks queuedLogCallbacks
	dispatcher := newLogDispatcher(callbacks.schedule, func(logBatch) { t.Fatal("disposed window received a batch") })
	dispatcher.add("queued before disposal")
	dispatcher.requestFlush()
	dispatcher.close()
	dispatcher.add("after disposal")
	dispatcher.finish(func() { t.Fatal("disposed window received process completion") })
	callbacks.run()
	dispatcher.close()
}

func TestDisposingDuringFinalLogDeliverySuppressesCompletion(t *testing.T) {
	var callbacks queuedLogCallbacks
	var dispatcher *logDispatcher
	dispatcher = newLogDispatcher(callbacks.schedule, func(logBatch) { dispatcher.close() })
	defer dispatcher.close()
	dispatcher.add("final backend line")
	dispatcher.finish(func() { t.Fatal("disposed window received process completion") })
	if !callbacks.run() {
		t.Fatal("final log delivery was not queued")
	}
	dispatcher.mu.Lock()
	defer dispatcher.mu.Unlock()
	if !dispatcher.stopped || dispatcher.onFinish != nil {
		t.Fatal("disposal retained its completion callback")
	}
}

func TestBackendLogReaderDrainsOversizedLinesAndContinues(t *testing.T) {
	input := strings.Repeat("long log ", 200_000) + "\nDEFAULT ROUTE → the tunnel\nlast without newline"
	var lines []string
	readBackendLogs(strings.NewReader(input), func(line string) { lines = append(lines, line) })
	if len(lines) != 3 || len(lines[0]) > logLineBytes || !strings.HasSuffix(lines[0], "[log line truncated]") || lines[1] != "DEFAULT ROUTE → the tunnel" || lines[2] != "last without newline" {
		t.Fatal("oversized log stopped pipe consumption or lost following messages")
	}
}

func TestLogReaderBoundsUnicodeAndHandlesEmptyEOF(t *testing.T) {
	reader := bufio.NewReaderSize(strings.NewReader(strings.Repeat("Ж", logLineBytes)+"\n"), 17)
	line, err := readLogLine(reader)
	if err != nil || len(line) > logLineBytes || !utf8.ValidString(line) {
		t.Fatal("bounded line broke UTF-8")
	}
	if line, err = readLogLine(reader); line != "" || err != io.EOF {
		t.Fatal("reader invented a line at EOF")
	}
}

func TestLogPendingByteBoundAppliesBeforeLineCountBound(t *testing.T) {
	var callbacks queuedLogCallbacks
	dispatcher := newLogDispatcher(callbacks.schedule, func(logBatch) {})
	defer dispatcher.close()
	for i := 0; i < 100; i++ {
		dispatcher.add(strings.Repeat("x", 1<<20))
	}
	dispatcher.mu.Lock()
	defer dispatcher.mu.Unlock()
	if dispatcher.bytes > logPendingBytes || dispatcher.count > logPendingBytes/logLineBytes || dispatcher.dropped == 0 {
		t.Fatal("large individual lines bypassed pending memory bounds")
	}
}
