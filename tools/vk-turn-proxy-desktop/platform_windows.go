//go:build windows

// SPDX-License-Identifier: MIT
//
package main

import (
	"errors"
	"fmt"
	"golang.org/x/sys/windows"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"unsafe"
)

func defaultDataDir() (string, error) {
	root, err := windows.KnownFolderPath(windows.FOLDERID_LocalAppData, 0)
	return filepath.Join(root, "VKTurnProxyWindows"), err
}

func secureDirectory(path string) error {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	return protectDirectory(path, "D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;FA;;;"+user.User.Sid.String()+")")
}

func protectDirectory(path, sddl string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	descriptor, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return err
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
}

func readSecret(path string, maxBytes int) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	encrypted, err := io.ReadAll(io.LimitReader(file, int64(maxBytes)+(1<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(encrypted) == 0 || len(encrypted) > maxBytes+(1<<20) {
		return nil, errors.New("invalid private storage size")
	}
	input := windows.DataBlob{Size: uint32(len(encrypted)), Data: &encrypted[0]}
	var output windows.DataBlob
	if err := windows.CryptUnprotectData(&input, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &output); err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(uintptr(unsafe.Pointer(output.Data))))
	plain := unsafe.Slice(output.Data, output.Size)
	defer clear(plain)
	if len(plain) > maxBytes {
		return nil, errors.New("invalid private storage size")
	}
	return append([]byte(nil), plain...), nil
}

func writeSecret(path string, data []byte) error {
	if len(data) == 0 {
		return errors.New("private storage is empty")
	}
	input := windows.DataBlob{Size: uint32(len(data)), Data: &data[0]}
	var output windows.DataBlob
	if err := windows.CryptProtectData(&input, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &output); err != nil {
		return err
	}
	defer windows.LocalFree(windows.Handle(uintptr(unsafe.Pointer(output.Data))))
	return writePrivate(path, unsafe.Slice(output.Data, output.Size))
}

func replaceFile(from, to string) error {
	source, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	target, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(source, target, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}

func platformCommand(backend string, args []string) *exec.Cmd {
	command := exec.Command(backend, args...)
	prepareBackendCommand(command)
	return command
}

func prepareBackendCommand(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
}

func requireDesktopPrivileges() error {
	if !windows.GetCurrentProcessToken().IsElevated() {
		return errors.New("administrator access is required to create the VPN tunnel")
	}
	return nil
}

func backendArguments(root string) []string {
	base, err := windows.KnownFolderPath(windows.FOLDERID_ProgramData, 0)
	if err != nil {
		return nil
	}
	return []string{"-state-file", filepath.Join(base, "VKTurnProxy", "network-state.json"), "-cred-cache", filepath.Join(root, "credentials.json"), "-log", filepath.Join(root, "client.log")}
}

func prepareMachineDirectory() error {
	root, err := windows.KnownFolderPath(windows.FOLDERID_ProgramData, 0)
	if err != nil {
		return err
	}
	if err := protectDirectory(filepath.Join(root, "VKTurnProxy"), "D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)"); err != nil {
		return fmt.Errorf("protect shared network state: %w", err)
	}
	return nil
}
