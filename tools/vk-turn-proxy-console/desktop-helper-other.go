//go:build !darwin

// SPDX-License-Identifier: MIT

package main

func platformDesktopHelper(args []string) (bool, int) { return false, 0 }
