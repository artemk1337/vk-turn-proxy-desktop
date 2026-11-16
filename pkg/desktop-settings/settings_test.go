// SPDX-License-Identifier: MIT

package desktopsettings

import "testing"

func TestDesktopContract(t *testing.T) {
	value := Defaults()
	if value.Languages != [2]string{"en", "ru"} || value.Themes != [2]string{"light", "dark"} {
		t.Fatalf("unexpected supported presentation settings: %+v", value)
	}
	if value.DefaultLanguage != "en" || value.DefaultTheme != "light" || value.DefaultTransport != "srtp" {
		t.Fatalf("unexpected defaults: %+v", value)
	}
	if value.LogLines != (LogLines{Default: 1000, Min: 100, Max: 10000}) || value.LogMaxBytes != 4<<20 || value.VKCheckSeconds != 300 || value.LogRetentionDays != 7 {
		t.Fatalf("unexpected desktop limits: %+v", value)
	}
	if value.RoutingUpdateSeconds != 86400 {
		t.Fatalf("unexpected routing update interval: %d", value.RoutingUpdateSeconds)
	}
	value.Languages[0] = "changed"
	if Defaults().Languages[0] != "en" {
		t.Fatal("changing a returned value mutated the desktop contract")
	}
}
