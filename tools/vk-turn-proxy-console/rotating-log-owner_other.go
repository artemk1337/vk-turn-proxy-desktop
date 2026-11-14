// SPDX-License-Identifier: MIT

//go:build !unix

package main

import "os"

func restoreLogOwner(*os.File, os.FileInfo) error { return nil }
