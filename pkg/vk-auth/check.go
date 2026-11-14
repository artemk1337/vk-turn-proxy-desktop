// SPDX-License-Identifier: MIT

package vkauth

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
	tlsclient "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
)

type Status string

const (
	Unknown     Status = "unknown"
	Valid       Status = "valid"
	Rejected    Status = "rejected"
	Unavailable Status = "unavailable"
)

type requestClient interface {
	Do(*fhttp.Request) (*fhttp.Response, error)
}

// Check validates a web session without joining a call or allocating TURN relays.
// It returns bounded states only, never tokens, cookies or remote response text.
func Check(ctx context.Context, input string) Status {
	client, err := tlsclient.NewHttpClient(tlsclient.NewNoopLogger(),
		tlsclient.WithTimeoutSeconds(15), tlsclient.WithClientProfile(profiles.Chrome_146),
		tlsclient.WithNotFollowRedirects())
	if err != nil {
		return Unavailable
	}
	defer client.CloseIdleConnections()
	return check(ctx, client, "https://login.vk.ru/?act=web_token", input)
}

func check(ctx context.Context, client requestClient, endpoint, input string) Status {
	cookies, err := ParseCookieInput(strings.NewReader(input))
	if err != nil {
		return Rejected
	}
	header, _, err := CookieHeader(cookies, time.Now())
	if err != nil {
		return Rejected
	}
	request, err := fhttp.NewRequestWithContext(ctx, "POST", endpoint, strings.NewReader("version=1&app_id=6287487"))
	if err != nil {
		return Unavailable
	}
	request.Header.Set("Cookie", header)
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Origin", "https://vk.ru")
	request.Header.Set("Referer", "https://vk.ru/")
	request.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/146.0.0.0 Safari/537.36")
	response, err := client.Do(request)
	if err != nil {
		return Unavailable
	}
	defer response.Body.Close()
	// A blocked request or transient server failure is not proof of an expired session.
	if response.StatusCode != 200 {
		return Unavailable
	}
	var result struct {
		Data struct {
			AccessToken string `json:"access_token"`
		} `json:"data"`
		Error json.RawMessage `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&result); err != nil {
		return Unavailable
	}
	if result.Data.AccessToken != "" {
		return Valid
	}
	// VK explicitly rejects cookies using an error payload. An unexpected response
	// stays unavailable so a network gateway cannot invalidate a stored session.
	var failure struct {
		Code int    `json:"error_code"`
		Type string `json:"error"`
	}
	if json.Unmarshal(result.Error, &failure) == nil && failure.Code == 5 {
		return Rejected
	}
	var kind string
	if json.Unmarshal(result.Error, &kind) == nil {
		switch kind {
		case "invalid_session", "invalid_cookie", "invalid_credentials", "access_denied":
			return Rejected
		}
	}
	return Unavailable
}
