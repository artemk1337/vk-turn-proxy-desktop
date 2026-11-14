package vkauth

import (
	"context"
	"io"
	"strings"
	"testing"

	fhttp "github.com/bogdanfinn/fhttp"
)

type fixtureClient struct {
	code  int
	body  string
	calls int
}

func (c *fixtureClient) Do(request *fhttp.Request) (*fhttp.Response, error) {
	c.calls++
	if request.Method != "POST" || request.URL.Path != "/" || request.URL.RawQuery != "act=web_token" {
		panic("unexpected endpoint")
	}
	if request.Header.Get("Cookie") != "remixsid=S; p=P" {
		panic("unexpected cookie header")
	}
	return &fhttp.Response{StatusCode: c.code, Body: io.NopCloser(strings.NewReader(c.body))}, nil
}
func TestSessionProbeStates(t *testing.T) {
	for _, test := range []struct {
		name, body string
		code       int
		want       Status
	}{
		{"valid", `{"data":{"access_token":"private"}}`, 200, Valid},
		{"expired", `{"error":{"error_code":5}}`, 200, Rejected},
		{"explicit expired", `{"error":"invalid_session"}`, 200, Rejected},
		{"limited", `{"error":{"error_code":6}}`, 200, Unavailable},
		{"server error", `{}`, 503, Unavailable},
		{"gateway html", `<html>`, 200, Unavailable},
		{"unexpected empty", `{}`, 200, Unavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &fixtureClient{code: test.code, body: test.body}
			if got := check(context.Background(), client, "https://login.vk.ru/?act=web_token", "remixsid=S; p=P"); got != test.want {
				t.Fatalf("got %s, want %s", got, test.want)
			}
			if client.calls != 1 {
				t.Fatal("expected exactly one read-only authentication request")
			}
		})
	}
}
func TestInvalidCookieNeverMakesRequest(t *testing.T) {
	client := &fixtureClient{}
	if got := check(context.Background(), client, "https://login.vk.ru/?act=web_token", "remixsid=S"); got != Rejected || client.calls != 0 {
		t.Fatal("invalid pair was sent")
	}
}
