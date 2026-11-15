//go:build darwin

// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"os/exec"
)

func macCommand(backend string, args []string) *exec.Cmd {
	return exec.Command(backend, append([]string{"--desktop-helper-client"}, args...)...)
}

func startPlatformCommand(backend string, args []string, output *os.File, stopFile string) (*exec.Cmd, func(), error) {
	parentReader, parentWriter, err := os.Pipe()
	if err != nil {
		return nil, nil, err
	}
	command := macCommand(backend, args)
	// EOF also stops the client if the GUI crashes during helper installation.
	command.ExtraFiles = []*os.File{parentReader}
	command.Stdout, command.Stderr = output, output
	if err := command.Start(); err != nil {
		parentReader.Close()
		parentWriter.Close()
		return nil, nil, err
	}
	parentReader.Close()
	return command, func() { parentWriter.Close() }, nil
}
