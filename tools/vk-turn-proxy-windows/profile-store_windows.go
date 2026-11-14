// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

func loadProfileStore(path string) (profileStore, error) {
	var store profileStore
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return store, nil
	}
	if err != nil {
		return store, err
	}
	defer file.Close()
	encrypted, err := io.ReadAll(io.LimitReader(file, (32<<20)+1))
	if err != nil {
		return store, err
	}
	if len(encrypted) == 0 || len(encrypted) > 32<<20 {
		return store, errors.New("Invalid profile storage size.")
	}
	input := windows.DataBlob{Size: uint32(len(encrypted)), Data: &encrypted[0]}
	var output windows.DataBlob
	if err := windows.CryptUnprotectData(&input, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &output); err != nil {
		return store, err
	}
	defer windows.LocalFree(windows.Handle(uintptr(unsafe.Pointer(output.Data))))
	plain := unsafe.Slice(output.Data, output.Size)
	defer clear(plain)
	if err := json.Unmarshal(plain, &store); err != nil {
		return store, err
	}
	return store, store.validate()
}

func saveProfileStore(path string, store profileStore) error {
	if err := store.validate(); err != nil {
		return err
	}
	plain, err := json.Marshal(store)
	if err != nil {
		return err
	}
	defer clear(plain)
	input := windows.DataBlob{Size: uint32(len(plain)), Data: &plain[0]}
	var output windows.DataBlob
	if err := windows.CryptProtectData(&input, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &output); err != nil {
		return err
	}
	defer windows.LocalFree(windows.Handle(uintptr(unsafe.Pointer(output.Data))))
	if err := secureDirectory(filepath.Dir(path)); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".profiles-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(unsafe.Slice(output.Data, output.Size)); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	from, err := windows.UTF16PtrFromString(file.Name())
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}
