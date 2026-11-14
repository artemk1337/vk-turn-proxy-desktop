// SPDX-License-Identifier: MIT

package main

import (
	"errors"
	"io"
	"os"
	"strings"

	wgprofile "github.com/artemk1337/vk-turn-proxy-desktop/pkg/wireguard-profile"
)

func loadWireGuardConfig(o *options) (*backupFile, error) {
	config, err := readManualInput(o.wgConfigPath)
	if err != nil {
		return nil, err
	}
	link := o.vkLink
	if o.vkLinkFile != "" {
		data, err := readManualInput(o.vkLinkFile)
		if err != nil {
			return nil, err
		}
		link = strings.TrimSpace(string(data))
	}
	raw, err := wgprofile.Build(wgprofile.Options{
		Config: string(config), VKLink: link, PeerAddress: o.peer,
		UseSrtp: o.serverTransport == "srtp",
	})
	if err != nil {
		return nil, err
	}
	return parseProfile(raw)
}

func readManualInput(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	config, err := io.ReadAll(io.LimitReader(file, wizardInputLimit+1))
	if err != nil {
		return nil, err
	}
	if len(config) > wizardInputLimit {
		return nil, errors.New("manual configuration input exceeds 4 MiB")
	}
	return config, nil
}
