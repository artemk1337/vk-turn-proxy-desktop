// SPDX-License-Identifier: MIT

package main

import (
	"strings"
	"testing"

	"github.com/lxn/win"
)

func TestNativeLogFloodRemainsBoundedAndScrollable(t *testing.T) {
	edit := newLogEdit(t)
	var view logView
	view.Reset(edit)
	var callbacks queuedLogCallbacks
	dispatcher := newLogDispatcher(callbacks.schedule, func(batch logBatch) {
		view.Append(edit, batch.text)
	})
	defer dispatcher.close()
	for round := 0; round < 20; round++ {
		for i := 0; i < 10_000; i++ {
			dispatcher.add("Backend diagnostic line under sustained load")
		}
		dispatcher.requestFlush()
		if !callbacks.run() {
			t.Fatal("log batch was not queued")
		}
	}
	if len(view.text) > logMaxBytes || edit.Text() != view.text || callbacks.maximum != 1 {
		t.Fatal("sustained logs bypassed the bounded renderer or callback queue")
	}
	edit.SendMessage(win.WM_VSCROLL, win.SB_TOP, 0)
	edit.SendMessage(win.EM_LINESCROLL, 0, 20)
	before := edit.SendMessage(win.EM_GETFIRSTVISIBLELINE, 0, 0)
	dispatcher.add("latest retained entry")
	dispatcher.requestFlush()
	callbacks.run()
	if !strings.HasSuffix(edit.Text(), "latest retained entry\r\n") {
		t.Fatal("new diagnostics stopped reaching the renderer")
	}
	// Trimming shifts the retained history up by at most one line here.
	after := edit.SendMessage(win.EM_GETFIRSTVISIBLELINE, 0, 0)
	if after > before || before-after > 1 {
		t.Fatalf("log flood stole the reader's scroll position: %d to %d", before, after)
	}
}
