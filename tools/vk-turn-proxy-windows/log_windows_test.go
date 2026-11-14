// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/lxn/walk"
	"github.com/lxn/win"
)

func newLogEdit(t *testing.T) *walk.TextEdit {
	t.Helper()
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	window, err := walk.NewMainWindow()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(window.Dispose)
	if err := window.SetLayout(walk.NewVBoxLayout()); err != nil {
		t.Fatal(err)
	}
	if err := window.SetBounds(walk.Rectangle{X: 0, Y: 0, Width: 640, Height: 360}); err != nil {
		t.Fatal(err)
	}
	edit, err := walk.NewTextEditWithStyle(window, win.WS_VSCROLL|win.ES_AUTOVSCROLL)
	if err != nil {
		t.Fatal(err)
	}
	if err := edit.SetBounds(walk.Rectangle{X: 0, Y: 0, Width: 600, Height: 260}); err != nil {
		t.Fatal(err)
	}
	if err := edit.SetReadOnly(true); err != nil {
		t.Fatal(err)
	}
	window.Show()
	return edit
}

func logTestLines(count int) string {
	var text strings.Builder
	for i := 0; i < count; i++ {
		fmt.Fprintf(&text, "line %05d sample entry\n", i)
	}
	return strings.TrimSuffix(text.String(), "\n")
}

func TestLogFollowsNewestLine(t *testing.T) {
	edit := newLogEdit(t)
	var view logView
	view.Reset(edit)
	view.Append(edit, logTestLines(300))
	before := edit.SendMessage(win.EM_GETFIRSTVISIBLELINE, 0, 0)
	if before == 0 {
		t.Fatal("log did not scroll to its latest lines")
	}
	view.Append(edit, "newest")
	if edit.SendMessage(win.EM_GETFIRSTVISIBLELINE, 0, 0) <= before {
		t.Fatal("new lines did not advance the following viewport")
	}
	if !view.followsTail(edit) {
		t.Fatal("log lost the bottom position")
	}
}

func TestLogPreservesHistoryViewportAndSelection(t *testing.T) {
	edit := newLogEdit(t)
	var view logView
	view.Reset(edit)
	view.Append(edit, logTestLines(300))
	edit.SetTextSelection(1000, 1030)
	edit.SendMessage(win.WM_VSCROLL, win.SB_TOP, 0)
	edit.SendMessage(win.EM_LINESCROLL, 0, 40)
	before := edit.SendMessage(win.EM_GETFIRSTVISIBLELINE, 0, 0)
	view.Append(edit, "newest")
	if after := edit.SendMessage(win.EM_GETFIRSTVISIBLELINE, 0, 0); after != before {
		t.Fatalf("history viewport moved from %d to %d", before, after)
	}
	start, end := edit.TextSelection()
	if start != 1000 || end != 1030 {
		t.Fatalf("reading selection changed to %d..%d", start, end)
	}
	if view.follow {
		t.Fatal("reading history unexpectedly enabled following")
	}
	if edit.Text() != view.text {
		t.Fatal("appending while reading history inserted text into the selection")
	}
}

func TestLogTrimmingPreservesRetainedHistory(t *testing.T) {
	edit := newLogEdit(t)
	var view logView
	view.Reset(edit)
	view.SetLimit(edit, 300)
	view.Append(edit, logTestLines(300))
	edit.SetTextSelection(6000, 6030)
	edit.SendMessage(win.WM_VSCROLL, win.SB_TOP, 0)
	edit.SendMessage(win.EM_LINESCROLL, 0, 200)
	beforeLine := int(edit.SendMessage(win.EM_GETFIRSTVISIBLELINE, 0, 0))
	beforeChar := int(edit.SendMessage(win.EM_LINEINDEX, uintptr(beforeLine), 0))
	incoming := logTestLines(100)
	plan := planLogAppend(view.text, incoming, view.limit)
	removedChars := utf16Length(view.text[:plan.removed])
	view.Append(edit, incoming)
	afterLine := int(edit.SendMessage(win.EM_GETFIRSTVISIBLELINE, 0, 0))
	afterChar := int(edit.SendMessage(win.EM_LINEINDEX, uintptr(afterLine), 0))
	if afterChar != beforeChar-removedChars {
		t.Fatalf("retained history anchor changed from %d to %d after removing %d", beforeChar, afterChar, removedChars)
	}
	start, end := edit.TextSelection()
	if start != 6000-removedChars || end != 6030-removedChars {
		t.Fatal("trim did not preserve selection in retained history")
	}
	if edit.Text() != view.text {
		t.Fatal("native log text differs from the bounded buffer")
	}
}

func TestAppendingToHiddenLogKeepsItHidden(t *testing.T) {
	edit := newLogEdit(t)
	var view logView
	view.Reset(edit)
	edit.SetVisible(false)
	view.Append(edit, "arrived while another tab was open")
	if win.IsWindowVisible(edit.Handle()) {
		t.Fatal("appending made the hidden log visible")
	}
}

func TestShowingLogPreservesHistoryWithoutNewEntries(t *testing.T) {
	edit := newLogEdit(t)
	var view logView
	view.Reset(edit)
	view.Append(edit, logTestLines(300))
	edit.SendMessage(win.WM_VSCROLL, win.SB_TOP, 0)
	edit.SendMessage(win.EM_LINESCROLL, 0, 40)
	before := edit.SendMessage(win.EM_GETFIRSTVISIBLELINE, 0, 0)
	edit.SetVisible(false)
	edit.SetVisible(true)
	view.FollowTail(edit)
	if edit.SendMessage(win.EM_GETFIRSTVISIBLELINE, 0, 0) != before {
		t.Fatal("reopening log discarded the reader's position")
	}
}

func TestChangingLogLimitImmediatelyTrimsNativeView(t *testing.T) {
	edit := newLogEdit(t)
	var view logView
	view.Reset(edit)
	view.Append(edit, logTestLines(1500))
	if strings.Count(view.text, "\n") != 1000 {
		t.Fatal("default line limit not applied")
	}
	view.SetLimit(edit, 200)
	if strings.Count(view.text, "\n") != 200 || edit.Text() != view.text {
		t.Fatal("native log not trimmed on setting change")
	}
}
