// SPDX-License-Identifier: MIT

package main

import (
	"io"
	"os"
	"testing"

	"github.com/artemk1337/vk-turn-proxy-desktop/pkg/proxy"
)

func TestDesktopTrafficIsOptIn(t *testing.T) {
	for _, test := range []struct {
		args []string
		want bool
	}{
		{nil, false},
		{[]string{"-desktop-traffic"}, true},
		{[]string{"-desktop-traffic=false"}, false},
	} {
		options, err := parseFlags(test.args)
		if err != nil || options.desktopTraffic != test.want {
			t.Fatalf("flags %v: options %+v, error %v", test.args, options, err)
		}
	}
}

func TestDesktopTrafficPreservesExactCountersOnStdout(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	previous := os.Stdout
	os.Stdout = writer
	printDesktopTraffic(proxy.Stats{TxBytes: 9223372036854775807, RxBytes: 123456789})
	os.Stdout = previous
	writer.Close()
	data, err := io.ReadAll(reader)
	if err != nil || string(data) != "desktop-traffic 9223372036854775807 123456789\n" {
		t.Fatalf("telemetry = %q, error %v", data, err)
	}
}
