// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

const maxUpdateBytes = 512 * 1024 * 1024

func updateAssetName(tag, platform, arch string) (string, error) {
	if _, valid := parseReleaseVersion(tag); !valid {
		return "", errors.New("Invalid release version.")
	}
	if arch != "amd64" && arch != "arm64" {
		return "", errors.New("Unsupported update architecture.")
	}
	suffix := ""
	switch platform {
	case "windows":
		suffix = "-setup.exe"
	case "linux":
		suffix = ".tar.gz"
	case "darwin":
		platform = "macos"
		suffix = ".zip"
	default:
		return "", errors.New("Unsupported update platform.")
	}
	return fmt.Sprintf("vk-turn-proxy-%s-%s-%s%s", platform, tag, arch, suffix), nil
}

func (a *App) DownloadUpdate() (State, error) {
	a.mu.Lock()
	if a.closing || a.updates.Status == "checking" || a.updates.URL == "" || a.updates.DownloadStatus == "choosing" || a.updates.DownloadStatus == "downloading" {
		state := a.snapshot()
		a.mu.Unlock()
		return state, nil
	}
	tag := a.updates.LatestVersion
	name, err := updateAssetName(tag, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		a.mu.Unlock()
		return State{}, err
	}
	a.updates.DownloadStatus = "choosing"
	a.mu.Unlock()
	path, err := a.updateSave(name)
	a.mu.Lock()
	defer a.mu.Unlock()
	a.updates.DownloadStatus = ""
	if err != nil {
		return a.snapshot(), err
	}
	if path == "" || a.closing {
		return a.snapshot(), nil
	}
	a.updates.DownloadStatus = "downloading"
	a.updates.Downloaded = 0
	a.updates.Total = 0
	a.updates.Path = ""
	go func() {
		client := &http.Client{Timeout: 30 * time.Minute}
		url := "https://github.com/artemk1337/vk-turn-proxy-desktop/releases/download/" + tag + "/" + name
		err := downloadUpdateFile(a.ctx, client, url, path, func(done, total int64) {
			a.mu.Lock()
			a.updates.Downloaded, a.updates.Total = done, total
			a.mu.Unlock()
		})
		a.mu.Lock()
		defer a.mu.Unlock()
		a.updates.DownloadStatus = "failed"
		if err == nil {
			a.updates.DownloadStatus = "downloaded"
			a.updates.Path = path
		}
	}()
	return a.snapshot(), nil
}

func downloadUpdateFile(ctx context.Context, client *http.Client, url, path string, progress func(int64, int64)) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	request.Header.Set("User-Agent", "VK-TURN-Proxy-Desktop")
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return errors.New("Could not download the update.")
	}
	if response.ContentLength > maxUpdateBytes {
		return errors.New("Update is too large.")
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".update-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	reader := io.LimitReader(response.Body, maxUpdateBytes+1)
	buffer := make([]byte, 128*1024)
	var total int64
	for {
		count, readErr := reader.Read(buffer)
		if total+int64(count) > maxUpdateBytes {
			return errors.New("Update is too large.")
		}
		if _, err := file.Write(buffer[:count]); err != nil {
			return err
		}
		total += int64(count)
		progress(total, response.ContentLength)
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	if total == 0 || response.ContentLength >= 0 && total != response.ContentLength {
		return io.ErrUnexpectedEOF
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
