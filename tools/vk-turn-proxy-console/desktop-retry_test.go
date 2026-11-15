// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestDesktopRetryPreservesStatusWithoutSecrets(t *testing.T) {
	for _, test := range []struct {
		err  error
		want string
	}{
		{errors.New("allocate: 486 Allocation Quota Reached"), "486"},
		{errors.New("failed https://example.test/?token=secret profile-private-key"), "connection"},
		{nil, ""},
	} {
		var out bytes.Buffer
		printDesktopRetry(&out, test.err, 1500*time.Millisecond)
		var event struct {
			Error        string `json:"error"`
			RetryAfterMs int64  `json:"retryAfterMs"`
		}
		raw := strings.TrimPrefix(out.String(), "desktop-retry ")
		if err := json.Unmarshal([]byte(raw), &event); err != nil || event.Error != test.want || event.RetryAfterMs != 1500 || strings.Contains(raw, "secret") {
			t.Fatalf("event %q: %+v, %v", raw, event, err)
		}
	}
}
