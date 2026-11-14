// SPDX-License-Identifier: MIT

package main

import "testing"

func TestProfileStoreValidation(t *testing.T) {
	profile := savedProfile{ID: "one", Name: "Home", Mode: "link", ProfileText: "vkturnproxy://import?data=test"}
	for _, test := range []struct {
		name  string
		store profileStore
		valid bool
	}{
		{"empty", profileStore{}, true},
		{"selected", profileStore{Profiles: []savedProfile{profile}, ActiveID: "one"}, true},
		{"missing selected", profileStore{ActiveID: "missing"}, false},
		{"duplicate", profileStore{Profiles: []savedProfile{profile, profile}}, false},
		{"unnamed", profileStore{Profiles: []savedProfile{{ID: "one", Mode: "link"}}}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := test.store.validate(); (err == nil) != test.valid {
				t.Fatalf("validate()=%v, valid=%v", err, test.valid)
			}
		})
	}
}

func TestSavedProfileConnection(t *testing.T) {
	p := savedProfile{Mode: "link", ProfileText: "  vkturnproxy://import?data=abc  "}
	data, err := p.connectionProfile()
	if err != nil || string(data) != "vkturnproxy://import?data=abc" {
		t.Fatalf("connectionProfile()=%q, %v", data, err)
	}
	p.ProfileText = " "
	if _, err := p.connectionProfile(); err == nil {
		t.Fatal("empty link accepted")
	}
	p.Mode = "unknown"
	if _, err := p.connectionProfile(); err == nil {
		t.Fatal("unknown mode accepted")
	}
}
