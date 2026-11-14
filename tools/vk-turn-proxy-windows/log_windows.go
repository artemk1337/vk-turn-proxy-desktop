// SPDX-License-Identifier: MIT

package main

import (
	"unsafe"

	"github.com/lxn/walk"
	"github.com/lxn/win"
)

type logView struct {
	text   string
	follow bool
}

func (v *logView) Reset(edit *walk.TextEdit) {
	v.text = ""
	v.follow = true
	edit.SetMaxLength(logMaxBytes)
	edit.SetText("")
}

// Append changes only the affected text and preserves a reader's viewport.
func (v *logView) Append(edit *walk.TextEdit, line string) {
	start, end := edit.TextSelection()
	follow := v.followsTail(edit) && start == end
	firstLine := edit.SendMessage(win.EM_GETFIRSTVISIBLELINE, 0, 0)
	firstChar := int(edit.SendMessage(win.EM_LINEINDEX, firstLine, 0))
	plan := planLogAppend(v.text, line)
	removedChars := utf16Length(v.text[:plan.removed])
	visible := win.IsWindowVisible(edit.Handle())
	if visible {
		edit.SendMessage(win.WM_SETREDRAW, 0, 0)
	}
	if removedChars > 0 {
		edit.SetTextSelection(0, removedChars)
		edit.ReplaceSelectedText("", false)
	}
	length := edit.TextLength()
	edit.SetTextSelection(length, length)
	edit.ReplaceSelectedText(plan.suffix, false)
	v.text = plan.text
	v.follow = follow
	if follow {
		edit.ScrollToCaret()
		edit.SendMessage(win.WM_VSCROLL, win.SB_BOTTOM, 0)
	} else {
		length := edit.TextLength()
		edit.SetTextSelection(min(max(0, start-removedChars), length), min(max(0, end-removedChars), length))
		targetChar := max(0, firstChar-removedChars)
		targetLine := int(edit.SendMessage(win.EM_LINEFROMCHAR, uintptr(targetChar), 0))
		currentLine := int(edit.SendMessage(win.EM_GETFIRSTVISIBLELINE, 0, 0))
		edit.SendMessage(win.EM_LINESCROLL, 0, uintptr(targetLine-currentLine))
	}
	if visible {
		edit.SendMessage(win.WM_SETREDRAW, 1, 0)
		win.InvalidateRect(edit.Handle(), nil, true)
	}
}

// FollowTail brings a newly shown log tab to its latest line when following.
func (v *logView) FollowTail(edit *walk.TextEdit) {
	start, end := edit.TextSelection()
	v.follow = v.followsTail(edit) && start == end
	if !v.follow {
		return
	}
	length := edit.TextLength()
	edit.SetTextSelection(length, length)
	edit.ScrollToCaret()
	edit.SendMessage(win.WM_VSCROLL, win.SB_BOTTOM, 0)
}

func (v *logView) followsTail(edit *walk.TextEdit) bool {
	if v.text == "" {
		return true
	}
	info := win.SCROLLINFO{CbSize: uint32(unsafe.Sizeof(win.SCROLLINFO{})), FMask: win.SIF_RANGE | win.SIF_PAGE | win.SIF_POS}
	if !win.GetScrollInfo(edit.Handle(), win.SB_VERT, &info) || info.NPage == 0 {
		return v.follow
	}
	return int64(info.NPos)+int64(info.NPage) >= int64(info.NMax)+1
}
