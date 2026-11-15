//go:build !darwin

// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"os/exec"
)

func startPlatformCommand(backend string, args []string, output *os.File, stopFile string) (*exec.Cmd, func(), error) {
	command := platformCommand(backend, args)
	command.Stdout, command.Stderr = output, output
	if err := command.Start(); err != nil {
		return nil, nil, err
	}
	return command, func() {}, nil
}
