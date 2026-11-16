package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	mathrand "math/rand"
	neturl "net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
	"github.com/google/uuid"
)

// vkAPIHost returns the FQDN used for all api.vk.* method calls
// (calls.getAnonymousToken, captchaNotRobot.*, etc).
//
// Defaults to api.vk.ru. VK_API_HOST overrides the host for diagnostics.
func vkAPIHost() string {
	if h := os.Getenv("VK_API_HOST"); h != "" {
		return h
	}
	return "api.vk.ru"
}

// vkCallJoinBase is the VK call-join link prefix the app rebuilds from the
// extracted call hash and hands to VK as the vk_join_link / link parameter
// (proxy.go, creds.go, creds_vkcalls.go). VK is migrating vk.com -> vk.ru;
// FLIPPED to vk.ru in build 170; vk.com still worked but VK is migrating.
// Centralised so this is a one-line change — revert to vk.com here if a live-VK
// test shows the anon path (api.vk.me link= and api.vk.ru vk_join_link=) rejects
// the vk.ru join link. NOTE: this is the link we SEND to VK — NOT the user-link
// parser, which accepts both domains.
const vkCallJoinBase = "https://vk.ru/call/join/"

// credExpiryBuffer is the safety margin before a TURN cred's expiry timestamp
// at which we consider the cred no longer fresh and start refreshing. The
// expiry comes from VK's TURN REST API (draft-uberti-behave-turn-rest):
// the credentials Username is "<expiry_unix_timestamp>:<key_id>", with the
// timestamp being the moment after which VK's TURN server rejects an
// ALLOCATE under the cred with error 401 — and that is all it gates.
// Measured 2026-09-19 against the VK relay: the same cred allocated 82 s
// before its timestamp and was refused 39 s after it (nine of nine expired
// creds refused, an unexpired control accepted beside each), while forty
// allocations made BEFORE their cred's timestamp were refreshed for 4h10m
// past it, every Refresh granted its 600 s — the re-authentication after a
// stale nonce included. A running session does not meet a 401 at its cred's
// expiry and needs no protection from one. The buffer is for the connection
// that (re)starts: it must never be handed a cred it can no longer allocate
// under, and the fresh-fetch path needs headroom to finish first (including
// a possible captcha solve, which can take many seconds on a hostile VK
// day), clock skew against VK included.
// 🚫 Do not shorten the buffer because sessions outlive their cred — their
// restarts do not; 🚫 and do not end a session because its cred expired.
//
// 30 min works out to ~7h 30m of usage on VK's typical 8h-validity creds —
// one fetch per cred-lifetime, no thrashing, plenty of margin if
// VK suddenly demands captcha during refresh.
const credExpiryBuffer = 30 * time.Minute

// parseCredExpiry extracts the expiry timestamp from a VK TURN cred Username.
// VK follows draft-uberti-behave-turn-rest: Username is
// "<unix_expiry_timestamp>:<key_id>". Returns (expiry, true) on parse success.
// On failure (malformed username) returns (zero, false) — caller should
// treat the slot as expired and refetch.
func parseCredExpiry(username string) (time.Time, bool) {
	idx := strings.IndexByte(username, ':')
	if idx <= 0 {
		return time.Time{}, false
	}
	ts, err := strconv.ParseInt(username[:idx], 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.Unix(ts, 0), true
}

// vkCredentials holds a VK API client_id/client_secret pair.
type vkCredentials struct {
	ClientID     string
	ClientSecret string
}

// vkCredentialsList contains the VK app credentials for the legacy
// calls.getAnonymousToken rotation. Two client_id gives failover and spreads
// per-app rate-limiting / captcha frequency. Each minted anon session is a
// distinct cred (own ~10-allocation quota), so 2 client_id do NOT cap conns —
// the credpool mints many distinct creds by re-fetching.
//
// 2026-05-31 — full rework after the build-149 force-legacy on-device test
// minted 0 creds. Two findings, both harness-verified from our own IP (see
// 29.05.2026/proxy-turn-vk-android-1.2.2/go_client iptest_test.go MINT TEST):
//
//  1. Dropped 3 app_ids (52461373 VK_WEB_VKVIDEO, 52649896 VK_MVK_VKVIDEO,
//     51781872 VK_ID_AUTH): they return error_code 3 "Unknown method passed"
//     for calls.getAnonymousToken (method not exposed to those apps) — never
//     produced a cred, and the wasted attempts amplified VK's per-IP captcha
//     escalation (checkbox→slider→BOT).
//
//  2. Replaced our stale secrets. Our old secrets for 6287487
//     (QbYic1K3lEV5kTGiqlq2) and 7879029 (aR5NKGmm03GYrCiNKsaw) SOLVE the
//     captcha fine but then VK returns error_code 10 "Internal server error"
//     on the post-captcha getAnonymousToken MINT — the token1 they yield is
//     mint-invalid. amurcanov's secret for 6287487 (MuAxFaKDYDOICzGnEOhp) and
//     their 2nd app 8202606 BOTH mint a call token cleanly from the same
//     IP/method (verified 2026-05-31: MINT OK 2/2). So the method is NOT
//     sunset — our secrets were stale. Matched amurcanov's exact pair.
//     (7879029 dropped: no known-good secret; re-add if one is found.)
var vkCredentialsList = []vkCredentials{
	{ClientID: "6287487", ClientSecret: "MuAxFaKDYDOICzGnEOhp"}, // VK_WEB — amurcanov's current working secret
	{ClientID: "8202606", ClientSecret: "lMRsTiMCyPnp5vfoldmn"}, // amurcanov's 2nd stable app_id
}

// CaptchaSolver is called when VK requires a captcha.
// It receives the captcha image URL and must return the user's answer.
// Returning an error aborts the credential fetch.
type CaptchaSolver func(imageURL string) (string, error)

// CaptchaRequiredError is returned when VK requires captcha and no solver is available.
type CaptchaRequiredError struct {
	ImageURL       string
	SID            string
	CaptchaTs      float64
	CaptchaAttempt float64
	Token1         string // step1 access_token — must be reused when retrying with captcha
	ClientID       string // VK app client_id — must be reused with savedToken1 (token1 is bound to this client_id)
	IsRateLimit    bool   // true when VK returned ERROR_LIMIT (PoW exhausted)
}

func (e *CaptchaRequiredError) Error() string {
	return fmt.Sprintf("captcha required: %s", e.ImageURL)
}

// CallUnavailableError is a NON-retryable VK error about the call/link itself
// (ended by the organizer, deleted, or an invalid join link) — as opposed to a
// captcha / rate-limit / transient network problem. When GetVKCreds sees one it
// STOPS immediately: no other client_id, no captcha, no free→legacy fallback —
// and surfaces Message (VK's error_msg) to the user. Contrast error_code 14
// "Captcha need" (retryable). Recognised by fatalCallError: the 9xxx range on
// the legacy calls.getAnonymousToken path (9000 "Call not found", 9008 "Join
// link is not valid", …) and the VK Calls free path's 95x block (951 "Call not
// found", 954 "Invalid join link").
type CallUnavailableError struct {
	Code    int
	Message string
}

func (e *CallUnavailableError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("call unavailable: %s (VK error %d)", e.Message, e.Code)
	}
	return fmt.Sprintf("call unavailable (VK error %d)", e.Code)
}

// TURNCreds holds TURN server credentials.
//
// Address vs Addresses: VK's vchat.joinConversationByLink response includes
// a turn_server.urls array — typically 2 endpoints on different /24 subnets
// (confirmed by build
// 53 logging). Until then we took only urls[0]. We now parse all of them
// into Addresses for diagnostics and possible future failover. We do NOT
// rotate per-conn: iOS includeAllNetworks=true allows only one exempt
// host (NEVPNProtocol.serverAddress), and routing the second TURN
// endpoint through the tunnel collapses throughput to ~0.5 Mbps via
// recursive routing. Clients must pin all relay traffic to the physical path.
// Address stays populated as Addresses[0] for back-compat with code that
// reads the singular field (cred caches, log strings, JSON marshaling).
type TURNCreds struct {
	Username  string
	Password  string
	Address   string   // host:port — primary address (= Addresses[0])
	Addresses []string // all VK-returned TURN endpoints, len >= 1
}

// isTransientNetworkError reports whether err looks like a transient network
// or DNS issue that may resolve itself within seconds. Empirically, on iOS
// the Network Extension's first DNS lookups right after startTunnel can fail
// with NXDOMAIN ("no such host") for the first ~30-100ms while the system
// resolver hasn't fully repointed at the physical Wi-Fi DNS yet — the same
// hostname resolves fine a second later. This predicate distinguishes those
// from genuine VK-side errors (HTTP 4xx/5xx, parse errors, etc.) so the
// retry loop in GetVKCreds only kicks in when retrying is plausibly useful.
func isTransientNetworkError(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "no such host") ||
		strings.Contains(s, "connection refused") ||
		strings.Contains(s, "network is unreachable") ||
		strings.Contains(s, "no route to host") ||
		// EADDRNOTAVAIL on an ESTABLISHED socket: its source address is gone
		// (an interface the phone lost after a path change; the kernel reports
		// it on the next read/write, ~50 s after the switch on 2026-09-07).
		// 🚫 NOT the "dial tcp …: connect: can't assign requested address" of
		// an IPv6-only Wi-Fi without an IPv4 source — that one fails every
		// dial in 30–200 ms and retrying it for 12 waves × 4 s would only
		// delay the same answer.
		strings.Contains(s, "read: can't assign requested address") ||
		strings.Contains(s, "write: can't assign requested address") ||
		strings.Contains(s, "i/o timeout") ||
		strings.Contains(s, "deadline exceeded") ||
		strings.Contains(s, "connection reset")
}

// GetVKCreds fetches TURN credentials from VK using a call invite link ID.
// captchaSolver may be nil; if nil and captcha is required, an error is returned.
// solvedCaptchaSID/solvedCaptchaKey: if non-empty, are from a previous captcha solve.
// solvedCaptchaKey is the success_token from captchaNotRobot.check.
// solvedCaptchaTs/solvedCaptchaAttempt are from the original captcha error response.
// savedToken1: if non-empty, reuse this access_token from step1 instead of fetching a new one
// (the captcha is tied to the original step2 call which used this token1).
// savedClientID: if non-empty, restrict the call to the matching credentials entry
// (the saved token1 is bound to a specific client_id, so on a captcha-retry we MUST
// reuse the same client). When empty, the normal client_id rotation+shuffle applies.
func GetVKCreds(linkID string, captchaSolver CaptchaSolver, solvedCaptchaSID, solvedCaptchaKey string, solvedCaptchaTs, solvedCaptchaAttempt float64, savedToken1, savedClientID string) (*TURNCreds, error) {
	// Non-anonymous cookie (logged-in / "VKAuth") path. When the user enables
	// cookie auth in Settings, this is the ONLY path — there is NO fallback to
	// the anonymous VK Calls / legacy paths (the whole point is to keep working
	// when VK has disabled anonymous join, so falling back would defeat it). On
	// failure we return the error; a cookie REJECTION (ErrCookieRejected:
	// expired/invalid) is latched into the cookie-auth fatal state so the
	// extension can stop the tunnel with a clear "re-login" message instead of
	// spinning. Cookie auth never triggers captcha, so the savedToken1 /
	// solvedCaptcha* retry parameters never apply here. See creds_vkcookie.go.
	if cookieAuthEnabled.Load() {
		// Cookie (VKAuth) path. This entry (no slot) serves slot 0 — used by the
		// pre-bootstrap probe to seed slot 0. The pool's per-slot fetches go
		// through fetchFreshCreds -> cookieCredForSlot(slot) so each slot stably
		// holds a distinct (call, relay) bucket. Single-relay creds; NO captcha;
		// NO anonymous fallback.
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		creds, err := cookieCredForSlot(ctx, 0, nil, nil, nil) // the app's probe: VK's relay; the pool's slot 0 takes the override's first when a list is set
		cancel()
		if err != nil {
			if errors.Is(err, ErrCookieRejected) {
				setCookieAuthFatal(err.Error())
			}
			return nil, fmt.Errorf("cookie auth: %w", err)
		}
		clearCookieAuthFatal()
		log.Printf("vk: success via cookie (logged-in) path (slot 0 seed)")
		return creds, nil
	}

	// VK Calls captcha-free path (added 2026-05-17 — see creds_vkcalls.go).
	//
	// Try this first on FRESH fetches only. Skip on captcha retry because:
	//   - savedToken1 is a login.vk.ru access_token bound to a vkCredentialsList
	//     client_id; VK Calls path uses a different VK Connect anonymous_token.
	//   - solvedCaptchaSID/Key are bound to a captchaNotRobot session that
	//     only exists on the legacy api.vk.ru/calls.getAnonymousToken path.
	// On any failure (including unexpected captcha gate appearing on the new
	// path), fall through to the legacy multi-client_id retry loop below.
	if savedToken1 == "" && solvedCaptchaSID == "" && savedClientID == "" {
		creds, err := getVKCredsViaVKCallsPath(linkID)
		if err == nil {
			log.Printf("vk: success via VK Calls captcha-free path")
			return creds, nil
		}
		// A fatal call/link error (ended, deleted, invalid link) hits the legacy
		// path identically — don't waste a captcha solve on it, stop now and
		// surface the VK message.
		if cu, ok := err.(*CallUnavailableError); ok {
			log.Printf("vk: VK Calls path — call unavailable (VK error %d: %s), not falling back", cu.Code, cu.Message)
			return nil, err
		}
		log.Printf("vk: VK Calls path failed, falling back to legacy: %v", err)
	}

	// Outer retry loop guards against transient network/DNS errors at the very
	// start of an extension launch — see isTransientNetworkError. We only loop
	// if EVERY client_id failed with such an error in the same wave; as soon
	// as one client_id reaches VK and gets a real response (success, captcha,
	// or HTTP/parse error), the network is up and further retries are wasted.
	//
	// Budget: 12 attempts × 4s delay between waves = up to ~44s of waiting,
	// well within the wgWaitBootstrapReady 120s budget (which itself has to
	// cover captcha-solver time after DNS comes back). Empirically the iOS
	// resolver after an airplane-mode toggle can take 30-60s before login.vk.ru
	// resolves cleanly — a 21s budget (the previous 8×3s setting) was observed
	// to give up while DNS was still recovering. The wider window absorbs that
	// without forcing the user to manually retry Connect.
	const maxNetworkRetries = 12
	const retryDelay = 4 * time.Second

	// Build the credentials list to walk. Normally we shuffle the full list
	// for per-app rate-limiting reasons, but when retrying with a saved
	// captcha solution + saved token1 we MUST stick to the original
	// client_id (token1 is bound to it; trying with a different client_id
	// would make step2 reject the captcha).
	var baseCreds []vkCredentials
	if savedClientID != "" {
		for _, vc := range vkCredentialsList {
			if vc.ClientID == savedClientID {
				baseCreds = []vkCredentials{vc}
				log.Printf("vk: pinned to client_id=%s for captcha-retry", savedClientID)
				break
			}
		}
		if len(baseCreds) == 0 {
			return nil, fmt.Errorf("savedClientID %q not in vkCredentialsList", savedClientID)
		}
	}

	var lastErr error
	for retry := 0; retry < maxNetworkRetries; retry++ {
		var creds []vkCredentials
		if baseCreds != nil {
			// Pinned mode — single client_id, no shuffle.
			creds = baseCreds
		} else {
			// Rotate through client_id/client_secret pairs to reduce per-app rate limiting.
			// Shuffle the list so each connection attempt uses a different order.
			creds = make([]vkCredentials, len(vkCredentialsList))
			copy(creds, vkCredentialsList)
			// DIAGNOSTIC (standalone tools/captcha_test only): replace the
			// whole credential list with VK_TEST_CREDS="id:secret,id:secret"
			// to probe whether a different client_id/secret (e.g. another
			// fork's) changes VK's captcha type/verdict. Unset in production.
			if tc := os.Getenv("VK_TEST_CREDS"); tc != "" {
				var override []vkCredentials
				for _, pair := range strings.Split(tc, ",") {
					kv := strings.SplitN(strings.TrimSpace(pair), ":", 2)
					if len(kv) == 2 && kv[0] != "" && kv[1] != "" {
						override = append(override, vkCredentials{ClientID: kv[0], ClientSecret: kv[1]})
					}
				}
				if len(override) > 0 {
					creds = override
					log.Printf("vk: VK_TEST_CREDS override active — %d custom client_id(s)", len(override))
				}
			}
			// DIAGNOSTIC (2026-05-17 api.vk.me test): filter to one specific
			// client_id if VK_CLIENT_ID_ONLY env var is set. Used to isolate
			// which client_id is captcha-gated on api.vk.me. Remove after
			// experiment resolves.
			if only := os.Getenv("VK_CLIENT_ID_ONLY"); only != "" {
				filtered := creds[:0]
				for _, vc := range creds {
					if vc.ClientID == only {
						filtered = append(filtered, vc)
					}
				}
				creds = filtered
				if len(creds) == 0 {
					return nil, fmt.Errorf("VK_CLIENT_ID_ONLY=%q not in vkCredentialsList", only)
				}
				log.Printf("vk: VK_CLIENT_ID_ONLY filter active — using only client_id=%s", only)
			}
			mathrand.Shuffle(len(creds), func(i, j int) { creds[i], creds[j] = creds[j], creds[i] })
		}

		allTransient := true
		for credIdx, vc := range creds {
			log.Printf("vk: trying credentials %d/%d: client_id=%s", credIdx+1, len(creds), vc.ClientID)
			result, err := getVKCredsWithClientID(linkID, vc, captchaSolver, solvedCaptchaSID, solvedCaptchaKey, solvedCaptchaTs, solvedCaptchaAttempt, savedToken1)
			if err == nil {
				log.Printf("vk: success with client_id=%s", vc.ClientID)
				return result, nil
			}
			// If it's a CaptchaRequiredError (needs WebView), return immediately — don't try other client_ids
			if _, isCaptcha := err.(*CaptchaRequiredError); isCaptcha {
				return nil, err
			}
			// A fatal call/link error (call ended/deleted, invalid link) won't be
			// fixed by another client_id — return immediately with the VK message.
			if _, isCallErr := err.(*CallUnavailableError); isCallErr {
				return nil, err
			}
			log.Printf("vk: failed with client_id=%s: %v", vc.ClientID, err)
			lastErr = err
			if !isTransientNetworkError(err) {
				allTransient = false
			}
		}

		// At least one client_id got a non-transient response from VK — the
		// network is fine, the issue is on VK's side. No point in retrying.
		if !allTransient {
			break
		}

		if retry < maxNetworkRetries-1 {
			log.Printf("vk: all %d client_ids failed with transient network error, retrying in %s (network retry %d/%d)",
				len(creds), retryDelay, retry+1, maxNetworkRetries-1)
			time.Sleep(retryDelay)
		}
	}
	return nil, fmt.Errorf("all %d client_ids failed, last error: %w", len(vkCredentialsList), lastErr)
}

func getVKCredsWithClientID(linkID string, vc vkCredentials, captchaSolver CaptchaSolver, solvedCaptchaSID, solvedCaptchaKey string, solvedCaptchaTs, solvedCaptchaAttempt float64, savedToken1 string) (*TURNCreds, error) {
	// Phase 10 (build 105) session-unified identity:
	//
	// All bootstrap requests below (login.vk.ru, api.vk.ru, calls.okcdn.ru)
	// now go through the SAME bogdanfinn HttpClient (Phase 9 Safari TLS
	// profile + shared cookie jar) that captcha_pow.go's solveCaptchaPoW
	// uses. Phase 9 + UA were the captcha-solver-only ID; bootstrap kept
	// leaking uTLS Chrome + random Chrome UA. VK's 2026-05-15 detection
	// update started catching the mismatch as BOT.
	//
	// UA: captured Safari WKWebView UA from vk_profile.json (same one
	// that computed the captured browser_fp). Falls back to generic Safari
	// iOS for cold start (pre-first-WebView). Previously: randomUserAgent()
	// returned a random Chrome desktop UA every call — guaranteed identity
	// inconsistency across requests AND with the Safari TLS profile.
	ua := GetSessionUserAgent()
	name := generateName()
	escapedName := neturl.QueryEscape(name)
	log.Printf("vk: identity — name: %s, UA: %s (Phase 10 session-unified)", name, ua)

	// Per-fetch device_id. VK keys the anonymous call participant on
	// (device_id, link) — proven by the harness MINT TEST (2026-05-31): an
	// EMPTY device_id (our old behavior) made VK return the SAME anon token for
	// every fetch of a given client_id, so ALL credpool slots shared ONE
	// 10-allocation quota → 486 "Allocation Quota Reached" on every conn past
	// the first ~10 (the "stuck at 20/20" bug). A fresh UUID per fetch mints a
	// DISTINCT anon participant → distinct TURN cred → its own 10-quota, exactly
	// like the free path (creds_vkcalls.go threads device_id the same way).
	// Threaded into every VK-side call below (get_anonym_token, getCallPreview,
	// and all calls.getAnonymousToken variants). NOT the OK.ru anonymLogin
	// device_id (step 3 below) — that's a separate identity with its own uuid.
	//
	// On an external WebView captcha retry (solvedCaptchaSID set) this generates
	// a fresh device_id that won't match the success_token's original request;
	// VK rejects the stale token and the internal PoW loop re-solves a fresh
	// captcha against THIS device_id, so the fetch self-reconciles.
	deviceID := uuid.New().String()

	// Fresh HTTP client (fresh cookie jar) per fetch — the ACTUAL cure for the
	// 20/20 cap. VK keys the legacy calls.getAnonymousToken anon token on the
	// SESSION COOKIE (remixstid), not device_id; reusing the shared
	// GetSessionClient made every fetch return the SAME token → all credpool
	// slots shared ONE 10-allocation TURN quota → 486 past ~10 conns. A fresh
	// session per fetch = a distinct VK identity = a distinct cred with its own
	// quota (harness-proven 2026-05-31: shared jar → 1 token, fresh jar →
	// distinct). Used in doRequest below AND passed to solveCaptchaPoW so the
	// captcha is solved in the SAME session that issued it.
	fetchClient := newFreshSessionClient()

	doRequest := func(data string, url string) (resp map[string]interface{}, err error) {
		client := fetchClient // fresh per-fetch session (see fetchClient above) — NOT the shared singleton
		// fhttp.NewRequest (NOT std http.NewRequest) — bogdanfinn HttpClient
		// only accepts fhttp.Request. fhttp is bogdanfinn's fork of net/http
		// that integrates with their HTTP/2 implementation.
		req, err := fhttp.NewRequest("POST", url, bytes.NewBuffer([]byte(data)))
		if err != nil {
			return nil, err
		}
		// Safari WKWebView header set — matches captcha_pow.go's vkReq
		// helper used for captchaNotRobot.*. Origin/Referer = id.vk.ru
		// because real Safari WebView captcha context loads from there;
		// VK API expects calls from that origin.
		req.Header.Set("User-Agent", ua)
		req.Header.Set("Accept", "*/*")
		req.Header.Set("Accept-Encoding", "gzip, deflate, br, zstd")
		req.Header.Set("Accept-Language", "en-GB,en;q=0.9")
		req.Header.Set("Cache-Control", "no-cache")
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", "https://id.vk.ru")
		req.Header.Set("Pragma", "no-cache")
		req.Header.Set("Priority", "u=3, i")
		req.Header.Set("Referer", "https://id.vk.ru/")
		req.Header.Set("Sec-Fetch-Dest", "empty")
		req.Header.Set("Sec-Fetch-Mode", "cors")
		req.Header.Set("Sec-Fetch-Site", "same-site")

		httpResp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer func() { _ = httpResp.Body.Close() }()

		// bogdanfinn auto-decompresses gzip; io.ReadAll yields plain bytes.
		body, err := io.ReadAll(httpResp.Body)
		if err != nil {
			return nil, err
		}

		err = json.Unmarshal(body, &resp)
		if err != nil {
			return nil, fmt.Errorf("unmarshal error: %w, body: %s", err, string(body))
		}
		return resp, nil
	}

	extractStr := func(resp map[string]interface{}, keys ...string) (string, error) {
		var cur interface{} = resp
		for _, k := range keys {
			m, ok := cur.(map[string]interface{})
			if !ok {
				return "", fmt.Errorf("expected map at key %q, got %T", k, cur)
			}
			cur = m[k]
		}
		s, ok := cur.(string)
		if !ok {
			return "", fmt.Errorf("expected string at end of path, got %T", cur)
		}
		return s, nil
	}

	// VK API version for the calls.* methods. Bumped 5.275 → 5.282 on 2026-06-24
	// (matching samosvalishe's working flow: const APIVersion = "5.282") as a
	// candidate fix for the post-captcha vchat.joinConversationByLink failure
	// (error.webrtc.auth.anonym_token.not_found) — the hypothesis is that a token
	// minted at the stale version isn't valid at join. Keep both calls.* in sync.
	step2URL := fmt.Sprintf("https://%s/method/calls.getAnonymousToken?v=5.282&client_id=%s", vkAPIHost(), vc.ClientID)

	// Step 1: get anonymous messages token
	// If savedToken1 is provided (captcha retry), reuse it instead of fetching a new one.
	var token1 string
	if savedToken1 != "" {
		token1 = savedToken1
		log.Printf("vk: reusing saved token1 for captcha retry")
	} else {
		data := fmt.Sprintf("client_id=%s&token_type=messages&client_secret=%s&version=1&app_id=%s&device_id=%s", vc.ClientID, vc.ClientSecret, vc.ClientID, deviceID)
		resp, err := doRequest(data, "https://login.vk.ru/?act=get_anonym_token")
		if err != nil {
			return nil, fmt.Errorf("step1: %w", err)
		}
		token1, err = extractStr(resp, "data", "access_token")
		if err != nil {
			return nil, fmt.Errorf("step1 parse: %w", err)
		}
	}

	// Step 1.5: call getCallPreview (warms up the session, as in reference impl)
	previewData := fmt.Sprintf("vk_join_link="+vkCallJoinBase+"%s&access_token=%s&device_id=%s", linkID, token1, deviceID)
	_, _ = doRequest(previewData, fmt.Sprintf("https://%s/method/calls.getCallPreview?v=5.282&client_id=%s", vkAPIHost(), vc.ClientID))

	// Step 2: get anonymous call token (with captcha retry)
	var token2 string
	var resp map[string]interface{}
	var err error
	step2Data := fmt.Sprintf("vk_join_link="+vkCallJoinBase+"%s&name=%s&access_token=%s&device_id=%s", linkID, escapedName, token1, deviceID)

	// buildCaptchaRetry builds the step2 body carrying a solved captcha. VK's
	// new error_code-14 format (2026-06-24) OMITS captcha_sid — it ships only
	// redirect_uri + session_token. So when sid is empty we send ONLY the
	// success_token and drop captcha_sid/captcha_key/ts/attempt (matching the
	// samosvalishe/cacggghp fix); an empty captcha_sid in the body confuses VK.
	buildCaptchaRetry := func(sid, successToken string, ts, attempt float64) string {
		if sid == "" {
			return fmt.Sprintf("vk_join_link="+vkCallJoinBase+"%s&name=%s&access_token=%s&success_token=%s&device_id=%s",
				linkID, escapedName, token1, neturl.QueryEscape(successToken), deviceID)
		}
		return fmt.Sprintf("vk_join_link="+vkCallJoinBase+"%s&name=%s&access_token=%s&captcha_key=&captcha_sid=%s&is_sound_captcha=0&success_token=%s&captcha_ts=%.3f&captcha_attempt=%d&device_id=%s",
			linkID, escapedName, token1, sid, neturl.QueryEscape(successToken), ts, int(attempt), deviceID)
	}

	// If we have a pre-solved captcha (success_token), include it. Gate on the
	// success_token only — the new VK format has no captcha_sid to require.
	if solvedCaptchaKey != "" {
		log.Printf("vk: retrying step2 with success_token (%d chars), captcha_sid=%q", len(solvedCaptchaKey), solvedCaptchaSID)
		step2Data = buildCaptchaRetry(solvedCaptchaSID, solvedCaptchaKey, solvedCaptchaTs, solvedCaptchaAttempt)
	}

	for attempt := 0; attempt < 3; attempt++ {
		resp, err = doRequest(step2Data, step2URL)
		if err != nil {
			return nil, fmt.Errorf("step2: %w", err)
		}

		// DIAGNOSTIC (2026-05-17 api.vk.me test): always dump step2 response
		// so we can see what api.vk.me returns differently from api.vk.ru.
		// Remove after the hypothesis is resolved.
		if respJSON, mErr := json.Marshal(resp); mErr == nil {
			log.Printf("vk: step2 response (host=%s, attempt=%d): %s", vkAPIHost(), attempt+1, string(respJSON))
		}

		// Check for captcha (VK error code 14)
		captchaSID, captchaImg, captchaTs, captchaAttempt := extractCaptcha(resp)
		// Gate on the captcha URL (redirect_uri), NOT captcha_sid: VK's new
		// error_code-14 format (2026-06-24) ships redirect_uri+session_token
		// with NO captcha_sid, so a sid-gate would skip the solver entirely.
		if captchaImg != "" {
			log.Printf("vk: captcha required (attempt %d), url: %s", attempt+1, captchaImg)

			// Try automatic PoW solver up to 3 times with fresh captcha sessions.
			const maxPoWRetries = 3
			powSolved := false
			currentImg := captchaImg
			currentSID := captchaSID
			currentTs := captchaTs
			currentAttempt := captchaAttempt
			var lastPowErr error
			// consecutiveEmptyShow tracks how many PoW attempts in a row came
			// back with show_captcha_type="" from the checkbox check — an
			// empirical signal that VK has no slider ready for this session.
			// After 2 such attempts in a row we short-circuit to WebView
			// instead of burning a third round-trip (saves ~3-5 seconds and
			// one captcha API call that just inflates VK's rate-limit bucket).
			consecutiveEmptyShow := 0

			for powTry := 1; powTry <= maxPoWRetries; powTry++ {
				log.Printf("vk: PoW attempt %d/%d", powTry, maxPoWRetries)
				powCtx, powCancel := context.WithTimeout(context.Background(), 30*time.Second)
				powToken, showType, reachedVK, powErr := solveCaptchaPoW(powCtx, fetchClient, currentImg, currentSID, ua)
				powCancel()
				lastPowErr = powErr

				if powErr == nil && powToken != "" {
					log.Printf("vk: PoW auto-solve succeeded on attempt %d (%d chars), retrying step2", powTry, len(powToken))
					step2Data = buildCaptchaRetry(currentSID, powToken, currentTs, currentAttempt)
					powSolved = true
					break
				}

				log.Printf("vk: PoW attempt %d/%d failed (show_captcha_type=%q): %v", powTry, maxPoWRetries, showType, powErr)

				// 🚨 `showType` IS ONLY VK'S ANSWER WHEN WE GOT FAR ENOUGH TO
				// BE ANSWERED. `solveCaptchaPoW` returns ("", "", err) on every
				// early failure — including a page it could not PARSE, where
				// `htmlSettings` is never even extracted — so on those paths the
				// empty string is OUR zero value, not a statement by VK.
				//
				// Reading it as one cost real time: when VK obfuscated the PoW
				// script (bundle 1.1.1395) every attempt died at the parse, and
				// this counter dutifully reported "VK has no slider ready" and
				// SKIPPED the third attempt — so the log accused VK's identity
				// verdict of a defect that was entirely ours, and the device log
				// and my own first reading both believed it.
				//
				// So the hint counts only when the attempt actually reached VK's
				// answer, which is exactly when there is no error to report.
				consecutiveEmptyShow = nextEmptyShowHint(reachedVK, showType, consecutiveEmptyShow)
				if consecutiveEmptyShow >= 2 {
					log.Printf("vk: %d consecutive attempts with show_captcha_type=\"\" — VK has no slider ready, skipping remaining attempts", consecutiveEmptyShow)
					break
				}

				if powTry < maxPoWRetries {
					freshData := fmt.Sprintf("vk_join_link="+vkCallJoinBase+"%s&name=%s&access_token=%s&device_id=%s", linkID, escapedName, token1, deviceID)
					freshResp, freshErr := doRequest(freshData, step2URL)
					if freshErr != nil {
						log.Printf("vk: failed to get fresh captcha for PoW retry: %v", freshErr)
						break
					}
					fSID, fImg, fTs, fAttempt := extractCaptcha(freshResp)
					if fImg == "" {
						token2, err = extractStr(freshResp, "response", "token")
						if err == nil {
							powSolved = true
						}
						break
					}
					currentSID = fSID
					currentImg = fImg
					currentTs = fTs
					currentAttempt = fAttempt
					log.Printf("vk: got fresh captcha for PoW retry %d/%d", powTry+1, maxPoWRetries)
				}
			}

			if powSolved {
				continue
			}

			// All PoW attempts exhausted — surface CaptchaRequiredError so
			// the CALLER decides what to do. Depending on the caller the
			// error may end up (a) shown as a WebView to the user, or (b)
			// swallowed by credPool.get via fallback to another fresh
			// slot's cred. This function does not know which.
			isRateLimit := lastPowErr != nil && strings.Contains(lastPowErr.Error(), "ERROR_LIMIT")
			log.Printf("vk: all %d PoW attempts failed, returning CaptchaRequiredError to caller (rateLimit=%v)", maxPoWRetries, isRateLimit)

			// CRITICAL: PoW solver consumed the captchaNotRobot.* API calls on
			// the current session_token (`baseParams := "session_token=%s..."`
			// in captcha_pow.go uses the same token that's embedded in the
			// captcha page URL). If we hand currentImg/currentSID to a
			// WebView for user solve, VK responds ERROR_LIMIT to that WebView's
			// captchaNotRobot.check because the session is burned. Fetch ONE
			// MORE fresh captcha (untouched by PoW) for whoever consumes this
			// error — WebView or stats-derived UI.
			//
			// Without this fix, every WebView open produced ERROR_LIMIT
			// (audit of 27-28.04 logs: 22/22 captcha-view JS check responses
			// were ERROR_LIMIT, 0 success_tokens, 0 user-solved captchas).
			//
			// Note: VK captcha bundle (not_robot_captcha.js) does have
			// anti-bot tooling (sandbox iframe pure fetch check) but it's
			// for analytics instrumentation, not bot blocking. The actual
			// issue is purely session_token consumption.
			freshData := fmt.Sprintf("vk_join_link="+vkCallJoinBase+"%s&name=%s&access_token=%s&device_id=%s", linkID, escapedName, token1, deviceID)
			if freshResp, freshErr := doRequest(freshData, step2URL); freshErr == nil {
				if fSID, fImg, fTs, fAttempt := extractCaptcha(freshResp); fImg != "" {
					log.Printf("vk: fetched untouched captcha for caller (was sid=%s, now sid=%s)", currentSID, fSID)
					currentSID = fSID
					currentImg = fImg
					currentTs = fTs
					currentAttempt = fAttempt
				}
			} else {
				log.Printf("vk: failed to fetch fresh captcha for caller (%v); returning burned one", freshErr)
			}

			if captchaSolver == nil {
				return nil, &CaptchaRequiredError{
					ImageURL:       currentImg,
					SID:            currentSID,
					CaptchaTs:      currentTs,
					CaptchaAttempt: currentAttempt,
					Token1:         token1,
					ClientID:       vc.ClientID,
					IsRateLimit:    isRateLimit,
				}
			}
			answer, err := captchaSolver(currentImg)
			if err != nil {
				return nil, fmt.Errorf("step2: captcha solver: %w", err)
			}
			log.Printf("vk: WebView captcha solver returned answer (%d chars), retrying", len(answer))
			step2Data = buildCaptchaRetry(currentSID, answer, currentTs, currentAttempt)
			continue
		}

		// Non-retryable call/link error (9xxx "Call not found" / "Join link is
		// not valid", …) — VK reached us fine, the call itself is gone. Stop and
		// surface the VK message instead of failing the token parse below.
		if fatal := fatalCallError(resp); fatal != nil {
			log.Printf("vk: step2 call unavailable (VK error %d: %s) — not retrying", fatal.Code, fatal.Message)
			return nil, fatal
		}

		token2, err = extractStr(resp, "response", "token")
		if err != nil {
			return nil, fmt.Errorf("step2 parse: %w", err)
		}
		break
	}
	if token2 == "" {
		return nil, fmt.Errorf("step2: failed after 3 captcha attempts")
	}

	// Step 3: OK.ru anonymous login
	data := fmt.Sprintf("session_data=%%7B%%22version%%22%%3A2%%2C%%22device_id%%22%%3A%%22%s%%22%%2C%%22client_version%%22%%3A1.1%%2C%%22client_type%%22%%3A%%22SDK_JS%%22%%7D&method=auth.anonymLogin&format=JSON&application_key=CGMMEJLGDIHBABABA", uuid.New())
	resp, err = doRequest(data, "https://calls.okcdn.ru/fb.do")
	if err != nil {
		return nil, fmt.Errorf("step3: %w", err)
	}
	token3, err := extractStr(resp, "session_key")
	if err != nil {
		return nil, fmt.Errorf("step3 parse: %w", err)
	}

	// Step 4: join conversation and get TURN creds
	data = fmt.Sprintf("joinLink=%s&isVideo=false&protocolVersion=5&anonymToken=%s&method=vchat.joinConversationByLink&format=JSON&application_key=CGMMEJLGDIHBABABA&session_key=%s", linkID, token2, token3)
	resp, err = doRequest(data, "https://calls.okcdn.ru/fb.do")
	if err != nil {
		return nil, fmt.Errorf("step4: %w", err)
	}

	// DIAGNOSTIC (2026-06-24): dump the raw okcdn join response so the standalone
	// tool (and on-device logs) show EXACTLY what calls.okcdn.ru returns for our
	// anonymToken — the wall is HERE (anonym_token.not_found), not the mint.
	// Mirrors the step2-response diagnostic above.
	if respJSON, mErr := json.Marshal(resp); mErr == nil {
		log.Printf("vk: step4 (okcdn vchat.joinConversationByLink) response: %s", string(respJSON))
	}

	user, err := extractStr(resp, "turn_server", "username")
	if err != nil {
		return nil, fmt.Errorf("step4 parse username: %w", err)
	}
	pass, err := extractStr(resp, "turn_server", "credential")
	if err != nil {
		return nil, fmt.Errorf("step4 parse credential: %w", err)
	}

	turnServer, ok := resp["turn_server"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("step4: turn_server not a map")
	}
	urls, ok := turnServer["urls"].([]interface{})
	if !ok || len(urls) == 0 {
		return nil, fmt.Errorf("step4: turn_server.urls empty")
	}
	// VK's vchat.joinConversationByLink returns a turn_server.urls array —
	// confirmed in vpn.wifi.7.log (build 53) to contain 2 distinct endpoints
	// on different /24 subnets. Extract them all for downstream rotation
	// (proxy.resolveTURNAddr distributes conns round-robin).
	log.Printf("vk: turn_server.urls count=%d", len(urls))
	var addresses []string
	for i, u := range urls {
		s, ok := u.(string)
		if !ok {
			log.Printf("vk: turn_server.urls[%d]=<non-string %T> — skipping", i, u)
			continue
		}
		clean := strings.Split(s, "?")[0]
		addr := strings.TrimPrefix(strings.TrimPrefix(clean, "turn:"), "turns:")
		log.Printf("vk: turn_server.urls[%d]=%s", i, addr)
		addresses = append(addresses, addr)
	}
	if len(addresses) == 0 {
		return nil, fmt.Errorf("step4: no usable turn_server.urls (all non-string?)")
	}

	return &TURNCreds{
		Username:  user,
		Password:  pass,
		Address:   addresses[0],
		Addresses: addresses,
	}, nil
}

// --- credPool: per-conn TURN credential cache ---
//
// Ported from turnbridge's poolCreds:
//
//   Commit that introduced it:
//     https://github.com/nullcstring/turnbridge/commit/72cd1d4a8f04eec0e5e210388d415a84777cd2e6
//   Current version:
//     https://github.com/nullcstring/turnbridge/blob/main/wireguard-apple/Sources/WireGuardKitGo/turn_proxy.go
//     (function poolCreds, ~lines 602-647 at time of porting)
//
// Adaptations for this codebase:
// - Per-connection affinity: each conn has its own slot in the pool. When
//   a conn's cred goes stale (per-entry TTL), only that conn refetches,
//   leaving other conns' fresh creds untouched. This is the key property
//   that keeps the tunnel alive while one conn's refetch is stuck waiting
//   for the user to solve captcha.
// - Degraded-mode fallback: if a conn's own fetch fails (captcha or 403),
//   fall back to round-robin over ANY fresh entry so the conn can still
//   come back up using another conn's cred.
// - Per-entry TTL (each slot tracks its own ts) instead of turnbridge's
//   global "wipe when any entry is 10 min old". Entries refresh independently.

// credPoolEntry is one slot in the pool — either filled (creds != nil)
// or empty. Credential validity and live leases are tracked independently.
type credPoolEntry struct {
	addr  string
	addrs []string // the relays this credential may be dialled on — VK's own, or the override's (relayhealth.go); addr is the first VK one, for the cache and the logs
	creds *TURNCreds
	ts    time.Time // when this slot was last filled; zero for empty slots

	// fetching: a goroutine is currently doing a VK fetch to populate
	// this slot. Other goroutines (get / background grower) skip the
	// fetch path for this slot and fall back instead of duplicating work.
	fetching bool

	// gen is the claim stamp of the fetch that owns this slot. A fetch
	// (tryFill, get()'s Phase 2) takes a fresh value from cp.nextGen when it
	// sets `fetching` (claimForFetchLocked) and presents it again before
	// publishing (ownsLocked). Every other write leaves the zero value and
	// invalidate() rebuilds the slice from zero entries, so a fetch that
	// began BEFORE a Resume reset the pool — or before invalidateEntry
	// cleared the slot, or before a later claim took it — finds its stamp
	// gone and does not overwrite what is there now. 🚨 2026-09-08, user's
	// review: without this a late grower fetch replaced a slot the new
	// connections had refilled and reset its active 10 → 0 — the sessions
	// lived on, unaccounted, and every later release, quota check and
	// path-change marking worked on a count that was wrong.
	gen uint64

	// active: number of conns currently using this slot's cred (i.e.
	// holding an active TURN allocation against it). VK enforces a strict
	// quota of 10 simultaneous allocations per cred set, so get() refuses
	// to hand out a cred whose active count is already 10 — even if the
	// slot is otherwise fresh — and the caller is steered to the next
	// available slot or to wait for one. The conn is responsible for
	// calling release() when the cred is no longer in use (typically when
	// the TURN session ends or the conn switches to a different slot).
	active int

	// allocated counts the allocations that SUCCEEDED on this credential
	// (both transports report one — quotabreaker.go); a fresh entry starts
	// at 0, a refill replaces the entry. A 486 on a credential with none is
	// the relay's refusal, with some its quota.
	allocated int

	// Per-relay lease history is separate from credential validity.
	relayHist
	on map[string]*relayHist
}

// credCacheVersion retains compatibility with existing credential files.
const credCacheVersion = 2

// credCacheMode names the kind of identity a pool mints and its cache file
// holds: VK's anonymous ones, or the logged-in account's (creds_vkcookie.go).
// A file of the other kind is not loaded — the identities must not mix: an
// account's credential used in anonymous mode deanonymises the account, and
// anonymous ones used under the account are the previous run's, not its own
// (the console, 2026-09-30: forty connections ran on the anonymous cache in
// cookie mode while the account's identity sat in two reserve slots).
// A file without the field is an
// anonymous one — every cache written before the field existed. The mode is
// the pool's from its creation (credCacheModeNow at newCredPool), not the
// global flag's at save time: the app turns cookie auth off at stop, before
// the pool's last save.
const (
	credCacheModeAnon   = "anon"
	credCacheModeCookie = "cookie"
)

// credCacheModeNow is the mode a pool created now mints in.
func credCacheModeNow() string {
	if cookieAuthEnabled.Load() {
		return credCacheModeCookie
	}
	return credCacheModeAnon
}

// credPeriodicSaveInterval bounds persistence lag for changed credentials.
const credPeriodicSaveInterval = 60 * time.Second

const pauseAcquireAfterPathEvent = 500 * time.Millisecond

// cascadeDetectionWindow + cascadePauseDuration implement adaptive pause-
// acquire for multi-event path cascades (vs single iOS dual-event sequence).
//
// Single handover: one PathMonitor event (or iOS dual-event 130-365ms
// apart) → pauseAcquireAfterPathEvent (500ms) is enough.
//
// Multi-event cascade: WiFi → LTE → other-WiFi → LTE within ~30-60 seconds
// (e.g., walking past a known WiFi network while moving from indoor to
// outdoor coverage, or jittery cellular causing reconnect bouncing).
// Each event marks the slots holding active conns. Between events, conns
// redistribute onto OTHER fresh slots — the next event then marks those
// too. Result: pool wiped after 3-4 events.
//
// Observed multi-event cascades and their gaps:
//
//	2026-05-13 20:01-20:02 — gaps 46s, 27s
//	2026-05-14 19:43-19:45 — gaps 51s, 36s
//	2026-05-14 14:30      — gaps ~3s + ~3s (with iface=other in middle,
//	                         handled by Variant B's 5s extended pause)
//
// On each path event we check `gap = now - lastPathEventAt`:
//
//	gap < 500ms                → iOS dual-event, normal pause refresh (500ms)
//	500ms ≤ gap < 90s           → CASCADE detected, set pause to 30s
//	gap ≥ 90s                  → isolated event, normal pause (500ms)
//
// 🚨 ARMING (2026-09-07): lastPathEventAt is recorded only by an event that
// found LIVE sessions (a slot with active > 0) or that itself detected a
// cascade, or that is the sub-500 ms follow-up of an armed event (an iOS
// burst's stamp keeps moving with its last event, as it always did — csqtt
// releases every lease within ms of the kick, so its follow-ups find
// nothing live) — never by a FIRST event on an idle pool. The tunnel's own
// start is such an event. Recording an idle startup notification would make
// the first real path switch look like a cascade and incur a 30-second pause.
// Real cascades re-arm even while all connections are parked. A cascade that
// starts after an outage emptied the pool is detected one event later.
//
// 90s window comfortably covers observed real cascades with margin (typical
// gap 25-60s). 30s pause covers the time conns would otherwise redistribute
// onto fresh slots between events. After 30s of "no path event" we assume
// network has stabilized.
//
// Trade-off: zero impact on normal handovers (no preceding event in 90s
// window → regular 500ms). Only abnormal multi-event sequences pay the
// 30s acquire-block. Conn retry loops see "credpool: paused for path-
// change settle" and wait, then resume normally.
const cascadeDetectionWindow = 90 * time.Second
const cascadePauseDuration = 30 * time.Second

// credCacheFile is the persisted pool cache. Each pool has one file containing
// TURN session credentials with approximately eight hours of validity.
// The caller must store it in a protected application directory.
// Runtime state (active count and fetching)
// is intentionally excluded — those are per-process and shouldn't survive
// across launches.
type credCacheFile struct {
	RecoveryGeneration string `json:"recovery_generation,omitempty"`
	// ReleasedAt certifies that every previous allocation was released.
	ReleasedAt int64            `json:"released_at,omitempty"`
	Version    int              `json:"version"`
	SavedAt    int64            `json:"saved_at"`
	Mode       string           `json:"mode,omitempty"` // credCacheModeAnon / credCacheModeCookie; absent in files from before it
	Creds      []credCacheEntry `json:"creds"`
	// RelayUsedAt retains account-wide relay quota history.
	RelayUsedAt map[string]int64 `json:"relay_used_at,omitempty"`
}

type credCacheEntry struct {
	Slot    int    `json:"slot"`
	Address string `json:"address"`
	// Addresses: every relay VK returned WITH this credential (Address is
	// the first). Since 2026-10-19; a file without it has the one address.
	Addresses []string `json:"addresses,omitempty"`
	Username  string   `json:"username"`
	Password  string   `json:"password"`
	// Usage timestamps protect against allocations left by an interrupted session.
	LastUsedAt int64            `json:"last_used_at,omitempty"`
	UsedOn     map[string]int64 `json:"used_on,omitempty"`
}

// credPool holds up to `size` independent TURN credential slots, one per
// connection index. Design:
//
//   - Lazy-first get(): callers prefer ANY fresh entry over blocking on a
//     VK fetch. Only when the pool has no fresh entry at all will a
//     caller invoke fetch inline.
//   - Background grower: a separate goroutine (see Proxy.growCredPool)
//     periodically picks an empty/stale slot and tries to fill it
//     without blocking any caller. That's how the pool grows to `size`
//     over time instead of at startup — which is what gave us the slow
//     ~100s serialized startup before.
//
// Thread safety: `mu` protects the pool slice and its entries. The VK
// fetch itself runs WITHOUT mu held (both get and tryFill drop the lock
// across the fetch call) so a long captcha solve on one slot cannot
// block fast-path get() calls on other conns' fresh slots.
type credPool struct {
	recoveryGeneration string
	cacheCooldowns     map[quotaHistoryKey]time.Time
	relayUsedAt        map[string]time.Time
	inheritedUntil     time.Time
	cacheRestarted     bool
	pendingRelease     bool
	cacheReleasedAt    time.Time
	onRelayMarked      func(addr string, stamp uint64) // told, outside the lock, of every relay newly marked UNREACHABLE with the proxy's hand-out stamp at the mark — the proxy ends the sessions handed that relay before it (relaymarkend.go)
	handSeq            uint64                          // the hand-out counter, moved UNDER THE LOCK at every address handed out (get → stampHandOutLocked); a mark reads it there and the notice carries it (relaymarkend.go)
	handStamp          map[int]uint64                  // the stamp of each connection's latest hand-out, by connIdx (recordHandOut reads it)
	mu                 sync.Mutex
	pool               []credPoolEntry // indexed by slot index; grown lazily up to size
	nextGen            uint64          // last claim stamp handed out — see credPoolEntry.gen
	size               int             // pool capacity, derived from NumConns via poolSizeForNumConns
	mode               string          // credCacheModeAnon / credCacheModeCookie — fixed at creation; what the cache file is stamped with and checked against

	// The relay health (relayhealth.go, credpool_relay.go): the standing of
	// every relay address by host:port, the user's override setting, each
	// connection's cursor among a credential's candidates, and the pause after
	// a mint that named only marked relays.
	health        *relayHealth
	override      turnOverride
	cursor        map[int]int
	deadMintUntil time.Time
	deadMintPause time.Duration
	healthTimer   *time.Timer
	healthWakeAt  time.Time
	// the override list's re-resolution (credpool_relay.go reresolveOverride)
	overrideResolving  bool
	overrideResolvedAt time.Time

	saverDone chan struct{} // closed after the periodic saver's final snapshot

	// holders is the pool's record of the leases still out — get() has handed
	// them out, release() has not yet taken them back — by WHAT each was taken
	// on: the slot and the credential it held then (leaseKey). It OUTLIVES the
	// entries. A refill of an in-use slot (the grower renews a credential
	// ~30 min before it expires, whether or not sessions still run on it),
	// invalidate() and invalidateEntry all replace an entry while its holders'
	// sessions live on; this record they never touch. Two readers:
	//
	//   - the path-change gate asks "is anything live?" — the per-entry counts
	//     answered "nothing live" with forty sessions up (the field run of
	//     2026-09-18);
	//   - every publish of a credential into a slot takes the entry's `active`
	//     FROM it (outstandingLocked), so a credential issued AGAIN into its
	//     slot — cookie mode returns the same username for a slot until the
	//     call's mint nears expiry, e.g. after a Resume's invalidate() — starts
	//     with its previous holders still seated instead of at 0, and their
	//     late releases free seats that were really taken (the user's review of
	//     build 409: by username alone such a release read as the NEW
	//     issuance's, made ten holders look like nine, announced room and let
	//     an eleventh onto the credential).
	//
	// Invariant: a slot that holds a credential has active ==
	// holders[{slot, its username}]; seatLocked and release move both
	// together. A key is deleted when its count reaches zero. Under cp.mu.
	holders map[leaseKey]int

	// seats is the same record BY RELAY: for each lease out, the relay it was
	// handed out for (get's pick) — what roomOnLocked compares with that
	// relay's quota, what a path change marks, what saveToDisk writes as
	// `used_on`. holders[k] == the sum of seats[k]. Under cp.mu.
	seats map[leaseKey]map[string]int

	// authErrors counts pion-reported 401/403 errors per slot since the
	// slot was last (re)filled. Reset to 0 on seedSlot, tryFill success,
	// and get's Phase 2 fetch success — i.e. anywhere a fresh cred lands
	// in the slot. Used by the pre-kill snapshot logging to attribute
	// mass kill events: if a slot has accumulated several auth errors
	// shortly before its conns die, the chain is "VK invalidated cred →
	// pion stops refreshing → allocation expires → conns silent → zombie
	// killer fires", and the cred should have been invalidated proactively.
	authErrors []atomic.Int64

	// freshLastTick records the entryIsFresh result per slot at the last
	// runPeriodicSave tick. logFreshTransitionsLocked compares current
	// against this snapshot and emits a log line for slots that flipped
	// out of fresh — these transitions are otherwise invisible because
	// entryIsFresh is a pure function whose result silently changes when
	// time.Now() crosses the credExpiryBuffer threshold relative to the
	// VK-encoded cred expiry. Without this log line, the UI's "available"
	// count drops without any explanation in the log stream (observed in
	// vpn.wifi-lte-wifi.1.log on 2026-05-12 — Pool went 8→7→6 over a
	// minute with zero markSaturated events because slots 9 and 11 were
	// crossing their 30m expiry-buffer thresholds silently).
	//
	// Kept under cp.mu like the rest of pool state.
	freshLastTick []bool

	// pauseAcquireUntil blocks credPool.get() with a "paused" error
	// while it's in the future. Set by MarkInUseSlotsForPathChange to
	// time.Now() + pauseAcquireAfterPathEvent on every path-change
	// event. Refreshed (extended) on each subsequent path event so a
	// dual-event iOS sequence (unsatisfied + satisfied within
	// 130-365ms) keeps the pool quiet through the whole transition.
	// See pauseAcquireAfterPathEvent comment for the full rationale.
	pauseAcquireUntil time.Time

	// pauseAcquireTimer fires broadcastSlotAvailable at pauseAcquireUntil
	// expiry so conns parked in runConnection's dormancy select wake up
	// the moment the pool unblocks, instead of sleeping out their full
	// dormantDuration. Without this, vpn.wifi-lte-wifi.1.log 2026-05-15
	// 13:03 cascade burned 4m20s waiting for the 5-min watchdog after
	// pauseAcquireUntil expired at +30s — every conn was sleeping in
	// dormancy with no signal that the credpool was ready.
	// (Re)armed by armPauseAcquireBroadcastLocked whenever pauseAcquire
	// Until is extended; idempotent (Stop+nil before re-arm).
	pauseAcquireTimer *time.Timer

	// lastPathEventAt is the timestamp of the most recent
	// MarkInUseSlotsForPathChange invocation that had live sessions to
	// protect or detected a cascade — NOT of every invocation (the
	// tunnel's own start reports a path with nothing live). Used for
	// adaptive cascade detection: if a new path event fires within
	// cascadeDetectionWindow of this timestamp (and not within iOS
	// dual-event range), we extend pauseAcquireUntil by
	// cascadePauseDuration instead of the default 500ms — see the
	// cascadeDetectionWindow comment.
	lastPathEventAt time.Time

	// fetch is the underlying credential fetcher. It must do all the work
	// previously inlined in resolveTURNAddr: build solver + pending-captcha
	// params, call GetVKCreds, parse TURN host:port, publish turnServerIP.
	// Returns (address "host:port", creds, err). On CaptchaRequiredError
	// the pool may choose to fall back to an existing entry instead of
	// surfacing the error.
	fetch func(allowCaptchaBlock bool, slot int) (string, *TURNCreds, error)

	// coldStartTarget is how many credential slots a cold pool needs before
	// get()'s Phase 2 stops minting and parks the rest of the herd to share —
	// ceil(NumConns/connsPerSlot), bounded by the pool size, set by
	// setColdStartTarget where NumConns is known (NewProxy, NewCredPool); the
	// grower's cold-start/maintenance switch reads the SAME number. 🚨 Until
	// 2026-09-08 get() derived it as ceil(size/4) — the inverse of the
	// ANONYMOUS pool sizing, which the cookie pool (2 × links slots) breaks:
	// 3 links ⇒ 6 slots ⇒ target 2, while 30 conns need 3 (call, relay)
	// buckets. After a path change that parked ten connections for 1m24s
	// until the grower's maintenance fill (vpn.vkauth.srtp.wifi.lte.0.log
	// 13:01:08 → 13:02:32, the log line `(2 ready+inflight >= 2 target)`).
	coldStartTarget int

	// cachePath persists the pool across client launches. Empty disables
	// persistence. The caller protects this file because it contains TURN
	// session credentials.
	cachePath string

	// saveMu serializes disk writes. saveToDisk snapshots pool state under
	// cp.mu (briefly), then releases cp.mu and writes under saveMu — this
	// avoids holding cp.mu during the (potentially slow) filesystem write
	// while still preventing concurrent writers from racing on the .tmp
	// file before the atomic rename.
	saveMu sync.Mutex

	// slotAvailableCh is closed-and-replaced whenever a slot transitions
	// toward usable: a fresh cred is seeded/fetched into a slot
	// (seedSlot, tryFill, get's Phase 2 fetch), or a conn release lowers
	// active count below the per-slot cap (release). runConnection's
	// retry-after-failure select listens on this channel alongside its
	// fallback timeout, so a conn parked in "no slot available" backoff
	// — or even in the longer dormancy after 3 short failures — wakes
	// up within a few milliseconds of any pool-state change instead of
	// sleeping out its randomised delay.
	//
	// The "close on signal, swap in a new chan" pattern gives broadcast
	// semantics: every conn currently waiting on the channel reference
	// it captured wakes simultaneously when the old chan closes. A
	// buffered single-slot channel would only wake one waiter.
	//
	// Saturation expiry is also signalled here: markSaturated arms a
	// time.AfterFunc that calls broadcastSlotAvailable when the
	// saturation window passes, so a parked conn wakes within ms of
	// the slot becoming usable again instead of sleeping through its
	// randomised 30s-3min dormancy. (Before this, vpn.wifi.2.log on
	// 2026-05-06 showed conn 0 idling 5+ min past slot 0's 11:28
	// expiry before its own backoff naturally fired.)
	//
	// Reads/writes of the chan field are guarded by cp.mu; close itself
	// runs after unlock so close-while-locked stalls are impossible.
	slotAvailableCh chan struct{}

	// quota counts actual relay refusals — see quotabreaker.go.
	quota quotaBreaker

	// The pool's own context: what outlives a moment (a held seat's release —
	// seatcool.go) stops with it. nil in a test's literal pool.
	ctx context.Context

	// Deferred seat releases are tracked independently from retry timing.
	seat seatStats
}

// leaseKey names what a lease was taken on: the slot and the credential
// (username) the slot held at that moment. The SLOT is part of it — in cookie
// mode the two relays of one call are two slots with ONE username, each its
// own quota bucket at VK — and so is the USERNAME: a refill puts another
// identity under the same slot number.
type leaseKey struct {
	slot     int
	username string
}

// leaseKeyOf is the ONE place a lease's key is built. creds is not nil.
func leaseKeyOf(slot int, creds *TURNCreds) leaseKey {
	return leaseKey{slot: slot, username: creds.Username}
}

func poolSizeForNumConns(n int) int {
	if n <= 0 {
		n = 1
	}
	// ceil(n * 4 / 10) = ceil(n * 2 / 5) — integer math.
	size := (n*2 + 4) / 5
	if size < 2 {
		size = 2
	}
	return size
}

// seedSlot installs an external credential or keeps a ready unrelated one.
// Matching cached credentials contribute their complete relay list and are deduplicated.
func (cp *credPool) seedSlot(slot int, addr string, creds *TURNCreds) {
	cp.mu.Lock()
	knownSeed := false
	for _, entry := range cp.pool {
		if entry.creds != nil && creds != nil && entry.creds.Username == creds.Username {
			knownSeed = true
			break
		}
	}
	if cp.mode != credCacheModeCookie && !knownSeed && len(cp.pool) > slot && cp.isFreshLocked(slot) {
		cp.mu.Unlock()
		log.Printf("credpool: seed for slot %d ignored — slot already has a fresh cred (likely loaded from disk)", slot)
		return
	}
	for len(cp.pool) <= slot {
		cp.pool = append(cp.pool, credPoolEntry{})
	}
	// Preserve a displaced credential in an empty slot when possible.
	var displaced *credPoolEntry
	if entry := cp.pool[slot]; entry.creds != nil && entry.creds.Username != creds.Username {
		copyEntry := entry
		displaced = &copyEntry
	}
	// The seed names ONE relay (the app's cache read hands over `address`); its
	// twin loaded from disk — the same credential, found by username below and
	// cleared as a duplicate — knows every relay VK returned with it. The seed
	// takes them: a seed with one relay has nothing to fail over to (the user's
	// phone, 2026-10-19: slot 0 alone stayed on the dead relay's one address
	// while the other slots had both).
	// 🚫 Not in cookie mode: there the same username sits in several slots
	// for several relays, each an (identity, relay) quota of its own.
	if creds != nil && cp.mode != credCacheModeCookie {
		for i := range cp.pool {
			if e := cp.pool[i]; e.creds != nil && e.creds.Username == creds.Username && len(e.creds.Addresses) > 0 {
				creds.Addresses = dedupeAddrs(append(append([]string{}, creds.Addresses...), e.creds.Addresses...))
				if creds.Address == "" {
					creds.Address = creds.Addresses[0]
				}
			}
		}
	}
	cp.placeLocked(slot, credPoolEntry{
		addr:   addr,
		addrs:  cp.candidatesForSlot(slot, creds),
		creds:  creds,
		ts:     time.Now(),
		active: cp.outstandingLocked(slot, creds), // the record's count — see credPool.holders
	})
	// Dedup: clear any OTHER slot holding the same VK cred set. This
	// happens when app-side reads creds-pool.json, picks an entry
	// (e.g. a never-used reserve slot) as the seed, and our own
	// loadFromDisk also loads that same entry into its original slot.
	// Without dedup the same cred ends up in two slots: pool count is
	// inflated, conns falling back to the duplicate slot compete for
	// the same VK 10-allocation quota, and background grower thinks
	// it has more cred sets than it really does.
	//
	// Username uniquely identifies a VK cred set per
	// draft-uberti-behave-turn-rest format (<expiry_unix>:<key_id>):
	// VK never reissues the same key_id+expiry tuple to a different
	// session, so equal usernames mean equal cred sets.
	duplicates := 0
	if creds != nil {
		for i := range cp.pool {
			if i == slot {
				continue
			}
			if cp.pool[i].creds != nil && cp.pool[i].creds.Username == creds.Username {
				cp.pool[i] = credPoolEntry{}
				duplicates++
			}
		}
	}
	// Cookie mode shares one relay quota across credentials. Deduplicate it.
	relayTwins := 0
	if creds != nil {
		for {
			twin := cp.cookieRelayTwinLocked(slot, creds)
			if twin < 0 {
				break
			}
			cp.pool[twin] = credPoolEntry{}
			relayTwins++
		}
		if displaced != nil && cp.mode == credCacheModeCookie {
			if k, d := cp.candidatesForSlot(slot, creds), cp.candidatesForSlot(slot, displaced.creds); len(k) == 1 && len(d) == 1 && k[0] == d[0] {
				displaced = nil
				relayTwins++
			}
		}
	}
	relocatedTo := -1
	if displaced != nil {
		for i := range cp.pool {
			if cp.pool[i].creds == nil {
				cp.placeLocked(i, *displaced)
				relocatedTo = i
				break
			}
		}
	}
	cp.mu.Unlock()
	if creds != nil {
		if exp, ok := parseCredExpiry(creds.Username); ok {
			log.Printf("credpool: seeded slot %d with externally-provided creds (addr=%s, expires in %s)",
				slot, addr, time.Until(exp).Round(time.Second))
		} else {
			log.Printf("credpool: seeded slot %d with externally-provided creds (addr=%s, malformed username — will refetch on first use)", slot, addr)
		}
		if duplicates > 0 {
			log.Printf("credpool: cleared %d duplicate slot(s) holding the same cred set as slot %d (disk-load + app-seed convergence)",
				duplicates, slot)
		}
		if relayTwins > 0 {
			log.Printf("credpool: cleared %d slot(s) holding the seed's relay %s under another identity (cookie mode: one quota per relay)",
				relayTwins, addr)
		}

		if relocatedTo >= 0 {
			log.Printf("credpool: relocated pre-seed credential from slot %d to slot %d",
				slot, relocatedTo)
		} else if displaced != nil {
			log.Printf("credpool: pre-seed credential at slot %d had no empty slot to relocate to, dropped",
				slot)
		}
	}
	if creds != nil && slot >= 0 && slot < len(cp.authErrors) {
		// Fresh cred lands in this slot — reset the auth-error tally so
		// subsequent kill snapshots reflect only this cred's history.
		cp.authErrors[slot].Store(0)
	}
	cp.saveToDisk()
	// Wake any conn parked in "no slot available" backoff: a fresh seed
	// just placed a usable cred into the pool. Also covers the dedup-
	// relocate paths above — even if creds==nil (no-op seed), other
	// goroutines may benefit from a re-check.
	cp.broadcastSlotAvailable()
}

// recordAuthError increments the per-slot auth-error counter. Called from
// turnLogger.maybeFlagAuthError when pion logs a 401/403 from VK on this
// slot's allocation. Safe with slot==-1 (no-op).
func (cp *credPool) recordAuthError(slot int) {
	if slot < 0 || slot >= len(cp.authErrors) {
		return
	}
	cp.authErrors[slot].Add(1)
}

// authErrorCount returns the current accumulated auth-error count for the
// slot since it was last refilled. -1 for invalid slot.
func (cp *credPool) authErrorCount(slot int) int64 {
	if slot < 0 || slot >= len(cp.authErrors) {
		return -1
	}
	return cp.authErrors[slot].Load()
}

// coldStartTargetFor is the cold-start target for numConns connections on a
// pool of size slots: ceil(numConns/connsPerSlot), never below 1 and never
// above the pool (a cookie pool of one link has two slots whatever N is).
func coldStartTargetFor(numConns, size int) int {
	target := (numConns + connsPerSlot - 1) / connsPerSlot
	if target < 1 {
		target = 1
	}
	if size >= 1 && target > size {
		target = size
	}
	return target
}

// setColdStartTarget fixes the pool's cold-start target from the connection
// count. Called once, before any goroutine uses the pool.
func (cp *credPool) setColdStartTarget(numConns int) {
	cp.mu.Lock()
	cp.coldStartTarget = coldStartTargetFor(numConns, cp.size)
	cp.mu.Unlock()
}

// coldStartTargetValue is the target the grower and get() share.
func (cp *credPool) coldStartTargetValue() int {
	cp.mu.Lock()
	defer cp.mu.Unlock()
	return cp.coldStartTarget
}

// newCredPool restores cached credentials and starts periodic persistence.
// The unused duration argument is retained for existing callers.
func newCredPool(ctx context.Context, size int, _ time.Duration, cachePath string, fetch func(bool, int) (string, *TURNCreds, error)) *credPool {
	if size < 1 {
		size = 1
	}
	cp := &credPool{
		ctx:       ctx,
		size:      size,
		cachePath: cachePath,
		fetch:     fetch,
		mode:      credCacheModeNow(),
		// Pre-size to `size` so later code (loadFromDisk, seedSlot,
		// the relocation loop in seedSlot, etc.) always sees the full
		// slot array with empty zero-entries in unused positions
		// rather than a truncated slice that hides real slots. Earlier
		// code grew the slice lazily with `for len(cp.pool) <= slot
		// { append... }`; that worked when callers always referenced
		// every slot eventually, but the seedSlot relocation loop had
		// no way to find an empty target slot if loadFromDisk only
		// populated slot 0 (cp.pool had len 1, so the loop saw only
		// the seed-target slot itself). Pre-sizing fixes that.
		pool:       make([]credPoolEntry, size),
		authErrors: make([]atomic.Int64, size),
		// Initial channel for the slot-available broadcast. Replaced
		// (and the old one closed) every time the pool changes in a
		// way that may unblock a parked conn — see broadcastSlotAvailable.
		slotAvailableCh: make(chan struct{}),
		// The pre-2026-09-08 inverse, right for the anonymous sizing
		// (size = ceil(N·2/5) ⇒ ceil(size/4) = ceil(N/10)) and for pools
		// built without a connection count (tests); production callers
		// overwrite it through setColdStartTarget.
		coldStartTarget: (size + 3) / 4,
		health:          newRelayHealth(),
		cursor:          map[int]int{},
	}
	if cachePath != "" {
		cp.loadFromDisk()
		// Periodic-save goroutine keeps lastUsedAt fresh on disk so a
		// kill-mid-session leaves the file reflecting current activity.
		// See credPeriodicSaveInterval comment.
		cp.saverDone = make(chan struct{})
		go cp.runPeriodicSave(ctx)
	}
	log.Printf("credpool: initialized with %d slots (expiry-buffer=%s, cache=%q)", size, credExpiryBuffer, cachePath)
	return cp
}

// runPeriodicSave wakes every credPeriodicSaveInterval and re-snapshots
// the pool to disk. The snapshot stamps LastUsedAt = now for every
// currently-active slot so the on-disk view stays within ~1 minute of
// "what slots were last holding live VK allocations" even when no fetch
// or invalidate event fires (steady-state). Exits when ctx is cancelled.
func (cp *credPool) runPeriodicSave(ctx context.Context) {
	ticker := time.NewTicker(credPeriodicSaveInterval)
	defer ticker.Stop()
	defer close(cp.saverDone)
	for {
		select {
		case <-ticker.C:
			// Surface silent fresh→not-fresh transitions BEFORE saving.
			// Brief locked block; saveToDisk takes its own locks so we
			// release cp.mu first to avoid nesting.
			cp.mu.Lock()
			cp.logFreshTransitionsLocked()
			cp.mu.Unlock()
			cp.saveToDisk()
		case <-ctx.Done():
			// Final save on shutdown so any in-memory lastUsedAt
			// updates from release() since the last tick make it
			// to disk before the goroutine exits.
			cp.saveToDisk()
			return
		}
	}
}

// loadFromDisk restores credentials and the quota history of interrupted sessions.
func (cp *credPool) loadFromDisk() {
	data, err := os.ReadFile(cp.cachePath)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("credpool: load: read %s: %v", cp.cachePath, err)
		}
		return
	}
	var f credCacheFile
	if err := json.Unmarshal(data, &f); err != nil {
		log.Printf("credpool: load: corrupt JSON in %s: %v — ignoring file", cp.cachePath, err)
		return
	}
	if f.Version != credCacheVersion {
		log.Printf("credpool: load: version %d != expected %d — ignoring file", f.Version, credCacheVersion)
		return
	}
	fileMode := f.Mode
	if fileMode == "" {
		fileMode = credCacheModeAnon // from before the field existed
	}
	if fileMode != cp.mode {
		log.Printf("credpool: load: the cache holds %s identities and this pool mints %s — ignoring file (it is rewritten at the next save)", fileMode, cp.mode)
		return
	}
	cp.recoveryGeneration = f.RecoveryGeneration

	released := cp.consumeReleasedCache(f)
	cp.mu.Lock()
	defer cp.mu.Unlock()

	now := time.Now()
	cp.restoreQuotaHistoryLocked(f, released, now)
	loaded, skipped := 0, 0
	for _, entry := range f.Creds {
		if entry.Slot < 0 || entry.Slot >= cp.size {
			skipped++
			continue
		}
		vk := dedupeAddrs(append([]string{entry.Address}, entry.Addresses...))
		creds := &TURNCreds{
			Username:  entry.Username,
			Password:  entry.Password,
			Address:   entry.Address,
			Addresses: vk,
		}
		exp, ok := parseCredExpiry(creds.Username)
		if !ok {
			log.Printf("credpool: load: slot %d malformed username, skipping", entry.Slot)
			skipped++
			continue
		}
		if exp.Sub(now) < credExpiryBuffer {
			log.Printf("credpool: load: slot %d expired or expiring within %s, skipping (expires in %s)",
				entry.Slot, credExpiryBuffer, time.Until(exp).Round(time.Second))
			skipped++
			continue
		}
		for len(cp.pool) <= entry.Slot {
			cp.pool = append(cp.pool, credPoolEntry{})
		}
		if twin := cp.cookieRelayTwinLocked(entry.Slot, creds); twin >= 0 {
			if texp, _ := parseCredExpiry(cp.pool[twin].creds.Username); !exp.After(texp) {
				log.Printf("credpool: load: slot %d holds the same relay %s as slot %d (cookie mode: one quota) — the older skipped",
					entry.Slot, entry.Address, twin)
				skipped++
				continue
			}
			log.Printf("credpool: load: slot %d holds the same relay %s as slot %d (cookie mode: one quota) — the older slot %d dropped",
				entry.Slot, entry.Address, twin, twin)
			cp.pool[twin] = credPoolEntry{}
			loaded--
			skipped++
		}
		cp.placeLocked(entry.Slot, credPoolEntry{
			addr:   entry.Address,
			addrs:  cp.candidatesForSlot(entry.Slot, creds),
			creds:  creds,
			ts:     now,
			active: cp.outstandingLocked(entry.Slot, creds), // the record's count — see credPool.holders

		})
		cp.restoreEntryHistoryLocked(entry, released)
		loaded++
		if until := cp.quotaReadyAtLocked(entry.Slot, cp.dialNowLocked(entry.Slot)); until.After(now) {
			log.Printf("credpool: load: slot %d waiting for previous VK allocations to expire, available in %s", entry.Slot, until.Sub(now).Round(time.Second))
		} else {
			log.Printf("credpool: load: slot %d ready (addr=%s, expires in %s)", entry.Slot, entry.Address, time.Until(exp).Round(time.Second))
		}
	}
	log.Printf("credpool: loaded %d slots from disk (%d skipped); file saved at %s",
		loaded, skipped, time.Unix(f.SavedAt, 0).Format(time.RFC3339))
	return
}

// saveToDisk snapshots the pool's filled slots and writes them to
// cachePath via tmp+rename. Called from any code path that mutates
// pool[].creds: seedSlot (slot 0 from app), tryFill / get's fetch
// branches (background grower and inline fetch), invalidateEntry (after
// a 401/403 drops a slot — the next snapshot just won't include it).
//
// Snapshot is taken under cp.mu (brief), but the actual file write runs
// under saveMu only — we don't want to block credPool callers on disk
// I/O. Two concurrent fetches completing simultaneously will serialize
// here without trampling each other's .tmp file.
func (cp *credPool) saveToDisk() {
	cp.saveCache(time.Time{})
}

func (cp *credPool) saveCache(releasedAt time.Time) {
	if cp.cachePath == "" {
		return
	}

	cp.saveMu.Lock()
	defer cp.saveMu.Unlock()
	now := time.Now()
	cp.mu.Lock()
	f := credCacheFile{
		RecoveryGeneration: cp.recoveryGeneration,
		Version:            credCacheVersion,
		SavedAt:            now.Unix(),
		Mode:               cp.mode,
	}
	for slot, entry := range cp.pool {
		if entry.creds == nil {
			continue
		}
		// For active slots stamp LastUsedAt as "now" — the slot is
		// holding live VK allocations at this very moment, which is the
		// most recent ground truth we have. release() will update the
		// in-memory lastUsedAt when active drops to 0; until then the
		// in-memory value may be stale (e.g. zero for a slot that was
		// filled by background grower and is currently in active use).
		var lastUsed int64
		if entry.active > 0 {
			lastUsed = now.Unix()
		} else if !entry.lastUsedAt.IsZero() {
			lastUsed = entry.lastUsedAt.Unix()
		}
		// Per relay (relayhist.go): "now" for a relay with seats out, else
		// the last seat's give-back when within the cooldown; last_used_at
		// stays the latest of them all for bootstrap seeding.
		var usedOn map[string]int64
		note := func(relay string, at time.Time) {
			if relay == "" || at.IsZero() || now.Sub(at) >= credSaturationCooldown {
				return
			}
			if usedOn == nil {
				usedOn = map[string]int64{}
			}
			if at.Unix() > usedOn[relay] {
				usedOn[relay] = at.Unix()
			}
			if at.Unix() > lastUsed {
				lastUsed = at.Unix()
			}
		}
		for relay, n := range cp.seats[leaseKeyOf(slot, entry.creds)] {
			if n > 0 {
				note(relay, now)
			}
		}
		for relay, h := range entry.on {
			note(relay, h.lastUsedAt)
		}
		// The entry's own history (relay not known — a file from before
		// used_on, seats the record could not place): kept under "*", every
		// relay's, so a relay of its own written beside it does not end it.
		note("*", entry.lastUsedAt)
		if cp.unattributedLocked(slot) > 0 {
			note("*", now)
		}
		f.Creds = append(f.Creds, credCacheEntry{
			Slot:       slot,
			Address:    entry.addr,
			Addresses:  entry.creds.Addresses,
			Username:   entry.creds.Username,
			Password:   entry.creds.Password,
			LastUsedAt: lastUsed,
			UsedOn:     usedOn,
		})
	}
	// Cookie mode: the relays' quota clocks — a slot in use holds seats NOW on
	// the relay it dials; clocks older than the cooldown say nothing.
	if cp.mode == credCacheModeCookie {
		for slot, entry := range cp.pool {
			if entry.creds != nil && entry.active > 0 {
				cp.noteRelayUsedLocked(slot, now)
			}
		}
		for relay, at := range cp.relayUsedAt {
			if now.Sub(at) < credSaturationCooldown {
				if f.RelayUsedAt == nil {
					f.RelayUsedAt = map[string]int64{}
				}
				f.RelayUsedAt[relay] = at.Unix()
			}
		}
	}
	cp.mu.Unlock()

	if !releasedAt.IsZero() && !cp.pendingRelease && !now.Before(cp.inheritedUntil) {
		cp.cacheReleasedAt = releasedAt
	}
	if !cp.cacheReleasedAt.IsZero() {
		f.ReleasedAt = cp.cacheReleasedAt.UnixNano()
	} else if cp.cacheRestarted || cp.pendingRelease {
		f = dirtyCredCache(f, now)
	}
	if err := cp.writeCache(f); err != nil {
		log.Printf("credpool: save: %v", err)
		return
	}
	if cp.pendingRelease {
		cp.cacheRestarted = true
		cp.pendingRelease = false
	}
}

func capText(capacity int) string {
	if capacity >= unlimitedCap {
		return "∞"
	}
	return fmt.Sprint(capacity)
}

// connsPerSlot is VK's hard quota of simultaneous TURN allocations on
// one cred set. Confirmed empirically: vpn.wifi.19.1.log showed conn
// 0-9 saturating cred 0, with conn 10+ ONLY succeeding on a separate
// cred set fetched into a different pool slot — even after 10+ minutes
// of retries on cred 0. So this is a strict cap, not a refilling
// bucket. The pool tracks per-slot active count and refuses to hand
// out a saturated slot's cred.
const connsPerSlot = 10

func (cp *credPool) pickAcquireSlotLocked(connIdx, ownSlot int, logSkips bool) (int, string, string) {
	now := time.Now()

	// Priority 1: ownSlot, unconditionally preferred when usable.
	if cp.isFreshLocked(ownSlot) && !cp.usableLocked(ownSlot) && logSkips {
		if cp.configuredOffLocked(ownSlot) {
			log.Printf("credpool: conn %d skipping slot %d (beyond the TURN override list)", connIdx, ownSlot)
		} else {
			log.Printf("credpool: conn %d skipping slot %d (every relay it names is marked unreachable: %s)",
				connIdx, ownSlot, cp.health.deadList(cp.candidatesLocked(ownSlot)))
		}
	}
	eligible := func(i int) (string, bool) {
		if i < 0 || i >= len(cp.pool) || !cp.hasRelayLocked(i) {
			return "", false
		}
		addr := cp.peekAddrLocked(i, connIdx)
		if addr == "" || !cp.freshOnLocked(i, addr) {
			return "", false
		}
		if until := cp.markLocked(i, addr); now.Before(until) {
			if logSkips {
				log.Printf("credpool: conn %d skipping slot %d (VK-saturated for %s more)",
					connIdx, i, until.Sub(now).Round(time.Second))
			}
			return "", false
		}
		if !cp.roomOnLocked(i, addr) {
			return "", false
		}
		return addr, true
	}
	// Priority 0: a slot whose relay has no quota (the list's host:port:0 —
	// the user's own relay) takes every connection while it answers, in both
	// modes (2026-10-30, the user's design). "While it answers" is the
	// health's untried — a silent one is not taken FIRST (cookie mode under a
	// fixed list keeps such a slot eligible in its own turn: the list is
	// cycled, not minted around — hasRelayLocked).
	for i := 0; i < cp.size && i < len(cp.pool); i++ {
		if addr, ok := eligible(i); ok && cp.override.unlimited(addr) && len(cp.health.untried([]string{addr})) > 0 {
			return i, addr, "unlimited relay"
		}
	}
	// Priorities 1–3 in TWO rounds: first among the slots whose relay for this
	// connection is UNTRIED (no attempt on it went unanswered on this path
	// since the last answer), then among the rest — a slot whose relay went
	// silent is dialled again only when no untried alternative is left (the
	// user's review of 48ccc45, 2026-11-01: in cookie mode a slot has ONE relay,
	// so conn 0's own slot was the silent own relay's whatever the cursor did,
	// and the bootstrap's first connection — alone until it succeeds — dialled
	// it for ever while the other entry's slot sat untried). In anonymous mode
	// the cursor already steers a connection past a silent candidate; the
	// rounds change nothing there.
	untried := func(addr string) bool { return len(cp.health.untried([]string{addr})) > 0 }
	kindOf := func(slot int) string {
		switch {
		case slot == ownSlot:
			return "own"
		case cp.pool[slot].active > 0:
			return "compact"
		}
		return "fresh"
	}
	// The first round: Priority 1 — ownSlot, preferred when usable; Priority
	// 2/3 — the other slots, max-active first (compact-fill), the lowest-index
	// idle one when every candidate has active=0.
	if addr, ok := eligible(ownSlot); ok && untried(addr) {
		return ownSlot, addr, "own"
	}
	bestSlot := -1
	bestActive := -1
	bestAddr := ""
	for i := 0; i < cp.size; i++ {
		if i == ownSlot {
			continue
		}
		addr, ok := eligible(i)
		if !ok || !untried(addr) {
			continue
		}
		// Strict > keeps the lowest-index slot when active counts tie
		// (iteration order is ascending).
		if cp.pool[i].active > bestActive {
			bestSlot = i
			bestActive = cp.pool[i].active
			bestAddr = addr
		}
	}
	if bestSlot >= 0 {
		return bestSlot, bestAddr, kindOf(bestSlot)
	}
	// The second round: every eligible slot's relay went silent, so none is
	// better than another — and the own slot preferred here dialled the same
	// silent relay for ever while the other had come back (the user's review
	// of ddb1fc1, 2026-11-01: O, O, O under `O:0,B` with both once silent,
	// the bootstrap's only connection). The connection's cursor, moved by
	// every unanswered attempt, walks the eligible slots instead — O, B, O,
	// B — so a relay that answers again is reached, and its answer clears
	// its record (the first round) and marks the other.
	var slots []int
	var addrs []string
	for i := 0; i < cp.size; i++ {
		if addr, ok := eligible(i); ok {
			slots = append(slots, i)
			addrs = append(addrs, addr)
		}
	}
	if len(slots) == 0 {
		return -1, "", ""
	}
	i := cp.cursor[connIdx] % len(slots)
	if i < 0 {
		i = -i
	}
	return slots[i], addrs[i], kindOf(slots[i])
}

type poolParkError struct {
	msg  string
	wake <-chan struct{}
}

func (e *poolParkError) Error() string { return e.msg }

func (cp *credPool) handOut(connIdx int, allowCaptchaBlock bool) (string, *TURNCreds, int, uint64, error) {
	if !cp.ensureCacheDirty() {
		return "", nil, -1, 0, errors.New("credpool: waiting to invalidate the previous cache release confirmation on disk")
	}
	cp.mu.Lock()

	// Path-change pause: while pauseAcquireUntil is in the future, refuse
	// new acquires so conn-retry-loops don't grab fresh slots in the gap
	// between iOS's dual PathMonitor events. See pauseAcquireAfterPathEvent
	// constant for the rationale. Conns see this error in their retry
	// loop, back off briefly, and try again after the pause expires.
	if !cp.pauseAcquireUntil.IsZero() && time.Now().Before(cp.pauseAcquireUntil) {
		remaining := time.Until(cp.pauseAcquireUntil).Round(time.Millisecond)
		wake := cp.slotAvailableCh
		cp.mu.Unlock()
		return "", nil, -1, 0, &poolParkError{msg: fmt.Sprintf("credpool: paused for path-change settle, %s remaining", remaining), wake: wake}
	}

	ownSlot := connIdx / connsPerSlot
	if ownSlot < 0 {
		ownSlot = 0
	}
	if ownSlot >= cp.size {
		ownSlot = cp.size - 1
	}
	// Make pool[ownSlot] addressable.
	for len(cp.pool) <= ownSlot {
		cp.pool = append(cp.pool, credPoolEntry{})
	}

	// candidatesOrdered enumerates pool slots starting with ownSlot
	// (preference), then the others in index order. Used by Phase 2
	// (fetch-target picking) below. Phase 1's acquire path uses
	// pickAcquireSlotLocked instead — it implements compact-fill,
	// which candidatesOrdered cannot express on its own.
	candidatesOrdered := func() []int {
		out := make([]int, 0, cp.size)
		out = append(out, ownSlot)
		for i := 0; i < cp.size; i++ {
			if i != ownSlot {
				out = append(out, i)
			}
		}
		return out
	}

	// Phase 1: any fresh slot with quota room? Prefer ownSlot; when
	// ownSlot isn't usable, fall back via compact-fill (consolidate
	// onto already-active slots) instead of first-by-index. See
	// pickAcquireSlotLocked for the selection logic + rationale (each
	// path event marks fewer slots when conns cluster).
	if slot, _, kind := cp.pickAcquireSlotLocked(connIdx, ownSlot, true); slot >= 0 {
		// The ADDRESS is chosen now, for this connection (credpool_relay.go):
		// the override list's or VK's, the one the pick was judged on — the
		// unmarked candidate at the connection's cursor (or a re-try's claim).
		addr, ok := cp.pickAddrLocked(slot, connIdx)
		if !ok { // no address after all (a re-try's claim ran out between the two looks): no seat, a park
			park := cp.relayParkLocked(connIdx, slot)
			cp.mu.Unlock()
			return "", nil, -1, 0, park
		}
		hand := cp.handOutLocked(connIdx, slot, addr) // the seat and the hand-out's stamp, under the lock the hand-out is made under (relaymarkend.go)
		e := cp.pool[slot]
		capacity := cp.capOnLocked(addr)
		seats := cp.seatsOnLocked(slot, addr)
		cp.mu.Unlock()
		var tag string
		switch kind {
		case "own":
			tag = "own slot"
		case "compact":
			tag = "fallback compact-fill"
		case "unlimited relay":
			tag = "the relay without a quota"
		default: // "fresh"
			tag = "fallback fresh slot"
		}
		log.Printf("credpool: conn %d acquired slot %d (%s, active=%d/%s, age %s) — relay %s",
			connIdx, slot, tag, seats, capText(capacity), time.Since(e.ts).Round(time.Second), addr)
		return addr, e.creds, slot, hand, nil
	}

	// Every fresh credential blocked by its relay marks, and a mint no way out
	// (the override list, or the dead-mint pause): wait (credpool_relay.go).
	if park := cp.healthParkLocked(); park != nil {
		cp.mu.Unlock()
		return "", nil, -1, 0, park
	}

	{
		ready := cp.countAvailableForLocked(connIdx)
		inFlight := 0
		for i := range cp.pool {
			if cp.pool[i].fetching {
				inFlight++
			}
		}
		coldStartTarget := cp.coldStartTarget // ceil(NumConns/connsPerSlot), see the field
		if coldStartTarget < 1 {
			coldStartTarget = 1
		}
		if ready+inFlight >= coldStartTarget {
			wake := cp.slotAvailableCh
			cp.mu.Unlock()
			return "", nil, -1, 0, &poolParkError{msg: fmt.Sprintf("credpool: cold-start cap (%d ready+inflight >= %d target) — parking to share instead of over-fetching", ready+inFlight, coldStartTarget), wake: wake}
		}
	}

	// Phase 2 selects an expiring or missing credential without an active fetch.
	now := time.Now()
	target := -1
	for _, slot := range candidatesOrdered() {
		if entryCredValid(cp.pool[slot]) {
			continue // Keep valid credentials, including those awaiting quota expiry.
		}
		if cp.pool[slot].fetching {
			continue
		}
		target = slot
		break
	}

	if target == -1 {
		for _, slot := range candidatesOrdered() {
			e := cp.pool[slot]
			blocked := e.creds != nil && !cp.health.hasCandidate(cp.candidatesLocked(slot)) // every relay it names is marked: a fresh mint may bring others
			benched := e.creds != nil && now.Before(cp.markLocked(slot, cp.peekAddrLocked(slot, connIdx)))
			if e.creds != nil && (benched || blocked) && e.active == 0 && !e.fetching {
				target = slot
				break
			}
		}
	}
	if target == -1 {
		if until := cp.nextQuotaReadyLocked(connIdx); !until.IsZero() {
			cp.mu.Unlock()
			return "", nil, -1, 0, &QuotaCooldownError{Until: until}
		}
		wake := cp.slotAvailableCh
		cp.mu.Unlock()
		return "", nil, -1, 0, &poolParkError{msg: "credpool: no slot available (all seats occupied or credentials being fetched)", wake: wake}
	}
	gen := cp.claimForFetchLocked(target)
	cp.mu.Unlock()

	// Inline fetch — runs WITHOUT mu so get() on other slots stays fast.
	fetchStart := time.Now()
	addr, creds, fetchErr := cp.fetch(allowCaptchaBlock, target)

	cp.mu.Lock()
	owns := cp.ownsLocked(target, gen)
	if owns {
		cp.pool[target].fetching = false
	}
	if fetchErr == nil {
		got, why := target, ""
		if owns {
			// The entry's count comes from the record of leases still out on
			// this very (slot, credential) — see credPool.holders. For a NEW
			// identity that is 0 and we take the first seat. A credential issued
			// AGAIN into its slot (cookie mode after a Resume's invalidate())
			// starts with its previous holders still seated; with all ten out
			// there is no seat for us either — an eleventh lease on one
			// credential is what earns a 486 — so the credential is kept and we
			// park: those holders' releases announce the room.
			held := cp.outstandingLocked(target, creds)
			cp.placeLocked(target, credPoolEntry{addr: addr, addrs: cp.candidatesForSlot(target, creds), creds: creds, ts: time.Now(), active: held, gen: gen})
			cp.noteVKAnsweredLocked(fetchStart)
			if !cp.noteMintLocked(target) {
				wake := cp.slotAvailableCh
				cp.mu.Unlock()
				cp.saveToDisk()
				return "", nil, -1, 0, &poolParkError{msg: fmt.Sprintf("credpool: the fresh credential in slot %d names only relays marked unreachable — parking", target), wake: wake}
			}
			// The seat is taken below, after the address is picked — and
			// judged against THAT relay's quota and the holders still out on
			// it (roomOnLocked).
		} else {
			cp.noteVKAnsweredLocked(fetchStart)
			if !cp.health.hasCandidate(cp.candidatesForSlot(-1, creds)) {
				if got, why = cp.placeIntoEmptySlotLocked(addr, creds, 0); got >= 0 {
					_ = cp.noteMintLocked(got)
					wake := cp.slotAvailableCh
					cp.mu.Unlock()
					cp.saveToDisk()
					return "", nil, -1, 0, &poolParkError{msg: fmt.Sprintf("credpool: the fresh credential in slot %d names only relays marked unreachable — parking", got), wake: wake}
				}
			} else {
				got, why = cp.placeIntoEmptySlotLocked(addr, creds, 0) // the seat below, after the pick
			}
		}
		if got < 0 {
			log.Printf("credpool: conn %d: slot %d changed hands during the fetch — fetched cred dropped (%s)", connIdx, target, why)
			fetchErr = fmt.Errorf("credpool: slot %d changed hands during the fetch, fetched cred dropped (%s)", target, why)
		} else {
			// Pick a relay before checking its live lease capacity.
			dial, ok := cp.pickAddrLocked(got, connIdx)
			if !ok {
				park := cp.relayParkLocked(connIdx, got)
				cp.mu.Unlock()
				cp.saveToDisk()
				return "", nil, -1, 0, park
			}
			if until := cp.quotaReadyAtLocked(got, dial); until.After(time.Now()) {
				cp.mu.Unlock()
				cp.saveToDisk()
				return "", nil, -1, 0, &QuotaCooldownError{Until: until}
			}
			// A credential issued AGAIN into its slot (cookie mode after a
			// Resume's invalidate()) starts with its previous holders still
			// seated; with every seat on this relay out there is none for us
			// — an eleventh lease on one credential is what earns a 486 — so
			// the credential is kept and we park: those holders' releases
			// announce the room.
			if !cp.roomOnLocked(got, dial) {
				held := cp.seatsOnLocked(got, dial)
				wake := cp.slotAvailableCh
				cp.mu.Unlock()
				if got < len(cp.authErrors) {
					cp.authErrors[got].Store(0)
				}
				cp.saveToDisk()
				log.Printf("credpool: conn %d: slot %d's credential was issued again with its %d previous holders still out — kept, no seat until they release", connIdx, got, held)
				return "", nil, -1, 0, &poolParkError{msg: fmt.Sprintf("credpool: slot %d's credential was issued again with its %d previous holders still out — parking until they release", got, held), wake: wake}
			}
			hand := cp.handOutLocked(connIdx, got, dial)
			filled := cp.countFreshLocked()
			cp.mu.Unlock()
			if got == target {
				log.Printf("credpool: conn %d fetched fresh cred into slot %d (%d/%d slots filled)",
					connIdx, target, filled, cp.size)
			} else {
				log.Printf("credpool: conn %d: slot %d changed hands during the fetch — fresh cred placed into slot %d (%d/%d slots filled)",
					connIdx, target, got, filled, cp.size)
			}
			if got < len(cp.authErrors) {
				cp.authErrors[got].Store(0)
			}
			cp.saveToDisk()
			// Inline fetch placed a fresh cred into a previously empty/
			// stale slot. The current conn took capacity 1/connsPerSlot;
			// the remaining connsPerSlot-1 slots of capacity are open for
			// other parked conns that lost the race for ownSlot.
			cp.broadcastSlotAvailable()
			return dial, creds, got, hand, nil
		}
	}

	// A failed fetch may still fall back to credentials filled concurrently.
	// Compact-fill applies here too: if the grower or another conn
	// filled a slot during our fetch window, prefer the most-active
	// one. logSkips=false because Phase 1's initial scan already
	// emitted any per-slot "VK-saturated" lines for these slots.
	if slot, _, kind := cp.pickAcquireSlotLocked(connIdx, ownSlot, false); slot >= 0 {
		dial, ok := cp.pickAddrLocked(slot, connIdx) // the same choice as every hand-out: the override's, the health's
		if !ok {
			park := cp.relayParkLocked(connIdx, slot)
			cp.mu.Unlock()
			return "", nil, -1, 0, park
		}
		hand := cp.handOutLocked(connIdx, slot, dial)
		e := cp.pool[slot]
		cp.mu.Unlock()
		var tag string
		switch kind {
		case "own":
			tag = "own slot"
		case "compact":
			tag = "fallback compact-fill"
		default: // "fresh"
			tag = "fallback fresh slot"
		}
		log.Printf("credpool: conn %d fetch into slot %d failed (%v), falling back to slot %d (%s, active=%d/%d, age %s), relay %s",
			connIdx, target, fetchErr, slot, tag, e.active, connsPerSlot, time.Since(e.ts).Round(time.Second), dial)
		return dial, e.creds, slot, hand, nil
	}
	cp.mu.Unlock()
	log.Printf("credpool: conn %d fetch into slot %d failed and no fallback available: %v",
		connIdx, target, fetchErr)
	return "", nil, -1, 0, fetchErr
}

// get hands connIdx an address through handOut, the hand-out's stamp dropped:
// for the callers that run no session on it (the console's and the app's
// probes, the tests). A session takes its address through resolveTURNAddr,
// which keeps the stamp as the session's generation.
func (cp *credPool) get(connIdx int, allowCaptchaBlock bool) (string, *TURNCreds, int, error) {
	addr, creds, slot, _, err := cp.handOut(connIdx, allowCaptchaBlock)
	return addr, creds, slot, err
}

func (cp *credPool) release(slot int, creds *TURNCreds, addr string) {
	if slot < 0 {
		return
	}
	cp.mu.Lock()
	if slot >= cp.size || slot >= len(cp.pool) {
		cp.mu.Unlock()
		return
	}
	// The lease comes off the record of what it was taken on. No such lease
	// out — a stray or doubled release, a nil credential — changes nothing.
	if creds == nil {
		cp.mu.Unlock()
		return
	}
	k := leaseKeyOf(slot, creds)
	switch out := cp.holders[k]; {
	case out == 0:
		cp.mu.Unlock()
		return
	case out == 1:
		delete(cp.holders, k)
	default:
		cp.holders[k] = out - 1
	}
	// ...and off the record BY RELAY: the relay this lease was handed out for.
	// A release that names no relay (or one the record does not know) comes
	// off the one relay the lease can have been on when there is one, else
	// counts against the entry's own history alone.
	if cp.seats[k][addr] == 0 {
		addr = cp.oneRelayWithSeatsLocked(k)
	}
	on := addr != "" && cp.seats[k][addr] > 0
	seatsBefore := cp.seats[k][addr]
	if on {
		cp.seats[k][addr]--
		if cp.seats[k][addr] == 0 {
			delete(cp.seats[k], addr)
		}
		if len(cp.seats[k]) == 0 {
			delete(cp.seats, k)
		}
	}
	cp.noteQuotaReleaseLocked(creds, addr, time.Now())
	if !cp.holdsLocked(slot, creds) {
		refilled := cp.pool[slot].creds != nil
		live := cp.liveLeasesLocked()
		cp.mu.Unlock()
		if refilled {
			// An emptied slot (a Resume's invalidate) is the ordinary case and
			// stays quiet; a slot holding another identity is the one to see.
			log.Printf("credpool: a release on slot %d names a credential the slot no longer holds — the refilled slot's count is left alone (%d lease(s) still live)", slot, live)
		}
		return
	}
	now := time.Now()
	// Decide whether this release will genuinely open a usable slot for
	// a parked conn. Three conditions must all hold; if any fails the
	// broadcast is a thundering-herd waste because get()'s Phase 1 would
	// skip the slot for the same reason that made the broadcast useless:
	//
	//   1. The relay this seat was on was at its cap before this release.
	//      Otherwise it already had room and conns weren't blocked on it.
	//   2. freshOnLocked — the credential isn't expired/expiring, and the
	//      clock on that relay has run out. Observed in vpn.wifi.5.log:
	//      slot 0's cred crossed the 30m expiry buffer ~4 min after
	//      startup; without this guard, every conn dying on that now-stale
	//      slot would wake 10 parked conns just to have them re-park.
	//   3. Not VK-saturated on that relay — get()'s Phase 1 explicitly
	//      skips a benched relay even when fresh and below cap.
	var slotBecomesUsable bool
	if on {
		slotBecomesUsable = seatsBefore == cp.capOnLocked(addr) &&
			cp.freshOnLocked(slot, addr) &&
			!now.Before(cp.markLocked(slot, addr))
	} else {
		slotBecomesUsable = cp.pool[slot].active == cp.capLocked(slot) &&
			cp.isFreshLocked(slot) &&
			!now.Before(cp.pool[slot].saturatedUntil)
	}
	if on && cp.seats[k][addr] == 0 {
		cp.histPtrLocked(slot, addr).lastUsedAt = now // the last seat on this relay given back
	}
	if cp.pool[slot].active > 0 {
		cp.pool[slot].active--
		if !on {
			cp.pool[slot].lastUsedAt = now // a seat whose relay is not known: the entry's own history, every relay's
		}
		if cp.pool[slot].active == 0 {
			if cp.configuredOffLocked(slot) {
				// The last seat of a slot whose relay the TURN override list
				// dropped (cookie mode): the slot may take a free entry now.
				cp.rebindOverrideSlotsLocked("the last lease on the dropped relay ended")
				slotBecomesUsable = cp.usableLocked(slot) && !now.Before(cp.markLocked(slot, cp.dialNowLocked(slot)))
			}
		}
	}
	cp.mu.Unlock()
	if slotBecomesUsable {
		cp.broadcastSlotAvailable()
	}
}

// outstandingLocked is how many leases are still out on (slot, creds) — what
// an entry's `active` starts from when that credential is published into
// that slot. Caller holds cp.mu.
func (cp *credPool) outstandingLocked(slot int, creds *TURNCreds) int {
	if creds == nil {
		return 0
	}
	return cp.holders[leaseKeyOf(slot, creds)]
}

// seatLocked takes one more seat on the credential slot holds: the entry's
// count and the record move together — the one way a seat is taken. Caller
// holds cp.mu; the slot holds a credential.
func (cp *credPool) seatLocked(slot int, relay string) {
	cp.pool[slot].active++
	if cp.holders == nil {
		cp.holders = make(map[leaseKey]int)
	}
	if cp.seats == nil {
		cp.seats = make(map[leaseKey]map[string]int)
	}
	k := leaseKeyOf(slot, cp.pool[slot].creds)
	cp.holders[k]++
	if cp.seats[k] == nil {
		cp.seats[k] = map[string]int{}
	}
	cp.seats[k][relay]++
}

// liveLeasesLocked is the number of leases still out, on whatever credential.
// Caller holds cp.mu.
func (cp *credPool) liveLeasesLocked() int {
	n := 0
	for _, c := range cp.holders {
		n += c
	}
	return n
}

// holdsLocked reports whether slot still holds the credential a lease named —
// by USERNAME, which identifies a VK cred set (seedSlot's dedup rule). The
// slot NUMBER does not: a refill, invalidate() and invalidateEntry put another
// identity, or none, under the same index. Everything a lease-holder tells the
// pool about ITS credential — a release, a 486, a 401 — goes through this
// check, as the allocation success mark does (noteAllocated). A nil or
// nameless credential names nothing. Caller holds cp.mu.
func (cp *credPool) holdsLocked(slot int, creds *TURNCreds) bool {
	if creds == nil || creds.Username == "" || slot < 0 || slot >= len(cp.pool) {
		return false
	}
	now := cp.pool[slot].creds
	return now != nil && now.Username == creds.Username
}

func (cp *credPool) saturationSnapshot() (saturated, total int, longest time.Duration) {
	cp.mu.Lock()
	defer cp.mu.Unlock()
	now := time.Now()
	total = cp.size
	for i := 0; i < cp.size && i < len(cp.pool); i++ {
		until := cp.markLocked(i, cp.dialNowLocked(i))
		if until.After(now) {
			saturated++
			remaining := until.Sub(now)
			if remaining > longest {
				longest = remaining
			}
		}
	}
	return
}

// markSaturated records an actual quota refusal without a local eligibility timer.
// The connection loop controls retries. Late responses do not alter new credentials.
func (cp *credPool) markSaturated(slot int, creds *TURNCreds, addr string) time.Duration {
	cp.mu.Lock()
	defer cp.mu.Unlock()
	if slot < 0 || slot >= cp.size || slot >= len(cp.pool) {
		return 0
	}
	if !cp.holdsLocked(slot, creds) {
		cp.noteStaleQuotaRefusalLocked()
		return 0
	}
	cp.noteQuotaRefusalLocked(slot, time.Now())
	return 0
}

// MarkInUseSlotsForPathChange resets health and briefly settles path transitions.
func (cp *credPool) MarkInUseSlotsForPathChange() {
	cp.mu.Lock()
	cp.pathChangedHealthLocked() // the relay marks were about the old network (credpool_relay.go)
	cp.mu.Unlock()
	cp.mu.Lock()
	defer cp.mu.Unlock()

	now := time.Now()
	live := len(cp.holders) > 0

	// Adaptive pause-acquire selection. See cascadeDetectionWindow comment
	// for the full rationale.
	pauseDur := pauseAcquireAfterPathEvent // default 500ms
	cascade, dual := false, false
	if !cp.lastPathEventAt.IsZero() {
		gap := now.Sub(cp.lastPathEventAt)
		switch {
		case gap < pauseAcquireAfterPathEvent:
			// iOS dual-event: a follow-up of the armed event. The burst's
			// stamp moves with it, so the burst is measured from its LAST
			// event — as before the arming gate. The native transport holds
			// every seat through a burst (the restart fires 1 s after the
			// last path-up), csqtt releases every lease within ms of the
			// kick and its follow-ups find nothing live: without this the
			// third event of a ≥ 500 ms burst would read as a cascade there.
			dual = true
		case gap < cascadeDetectionWindow:
			// Cascade detected — previous event was 500ms-90s ago,
			// not iOS dual-event (< 500ms) and not isolated (≥ 90s).
			// Extend pause to block conn redistribution until network
			// stabilises.
			pauseDur = cascadePauseDuration
			cascade = true
			log.Printf("credpool: path-change cascade detected (gap %v from prev event) — extending pause to %v",
				gap.Round(time.Millisecond), cascadePauseDuration)
		}
	}
	deadline := now.Add(pauseDur)
	if deadline.After(cp.pauseAcquireUntil) {
		cp.pauseAcquireUntil = deadline
		cp.armPauseAcquireBroadcastLocked()
	}
	// Arm the detector only from an event that had something to protect,
	// that continued a chain, or that followed an armed event within the
	// dual-event range — see the ARMING note on cascadeDetectionWindow. A
	// first event on an idle pool (the tunnel's own start report; the first
	// event after a long outage killed every session) records nothing, so the
	// next real switch is judged on its own. 🚫 Not `len(markedSlots) > 0`: a
	// start on a warm cache marks disk-loaded "active-recent" slots with
	// active 0 and would arm exactly as before.
	if live || cascade || dual {
		cp.lastPathEventAt = now
	} else {
		log.Printf("credpool: path event with nothing live — not counted for cascade detection")
	}
}

// armPauseAcquireBroadcastLocked (re)schedules a one-shot broadcast on
// slotAvailableCh for the current pauseAcquireUntil deadline. Caller must
// hold cp.mu.
//
// This is what wakes conns parked in runConnection's dormancy select
// (proxy.go:1606) at the moment the credpool unblocks. Without it, a conn
// that entered dormancy during a path-change cascade waits out its full
// random dormantDuration before retrying — even if pauseAcquireUntil
// expired earlier. Empirically surfaced by vpn.wifi-lte-wifi.1.log
// 2026-05-15 13:03 where 30 conns idled 4m20s after the 30s cascade pause
// expired, recovery only happening when the 5-min watchdog kicked in.
//
// Stops any previously-armed timer first so re-arming on extension (next
// path event arriving before the prior pause expired) doesn't double-fire
// or fire at the OLD deadline. Stop+nil is safe even if the old timer
// already fired — Stop returns false in that case, no panic.
//
// Spurious broadcasts are harmless by broadcastSlotAvailable's design:
// a conn that wakes only to find no usable slot just re-parks on the
// next channel.
func (cp *credPool) armPauseAcquireBroadcastLocked() {
	if cp.pauseAcquireTimer != nil {
		cp.pauseAcquireTimer.Stop()
		cp.pauseAcquireTimer = nil
	}
	delay := time.Until(cp.pauseAcquireUntil)
	if delay <= 0 {
		return
	}
	cp.pauseAcquireTimer = time.AfterFunc(delay, cp.broadcastSlotAvailable)
}

// ExtendPauseAcquireForTransition extends pauseAcquireUntil without marking
// any slots. Used for path events that signal "transition in progress, real
// network not yet stable" — specifically iOS PathMonitor satisfied events
// with iface=other (which empirically means our own TUN device becoming
// os-default during the brief window between physical interfaces).
//
// Motivation: vpn.over24h.log 2026-05-13 15:26 outage. Sequence was:
//
//	T+0     requiresConnection cellular (Event 1)
//	T+162ms satisfied iface=other os-default=192.168.102.4 (our TUN!)
//	T+3.3s  satisfied iface=cellular (Event 3 — real new path)
//
// MarkInUseSlotsForPathChange on Event 1 correctly marked 6 active slots
// and set pauseAcquireUntil = now + 500ms. By the time Event 2 fired
// (162ms after Event 1) the pause was still alive. But the 500ms pause
// expired before Event 3 (3.3s later), so 26 conns acquired fresh slots
// 0/1/2 during the gap, then those allocations dropped/failed/got 486 →
// pool-wide saturation cascade → 10-minute outage.
//
// The proper fix isn't to call MarkInUseSlotsForPathChange on the .other
// event — there are no NEW active slots to mark (the slots are the same
// ones marked at Event 1). Instead we just extend the pause to cover the
// transition window so conns DON'T acquire during the misleading recovery.
//
// Existing pause-set-via-MarkInUseSlots stays untouched. This function
// only extends. Idempotent and safe to call multiple times — uses max().
func (cp *credPool) ExtendPauseAcquireForTransition(d time.Duration) {
	cp.mu.Lock()
	defer cp.mu.Unlock()
	deadline := time.Now().Add(d)
	if deadline.After(cp.pauseAcquireUntil) {
		cp.pauseAcquireUntil = deadline
		cp.armPauseAcquireBroadcastLocked()
	}
}

// tryFill fetches one missing or expiring credential while preventing overlapping fetches.
func (cp *credPool) tryFill(slot int, allowCaptchaBlock bool, abortIfAvailableGTE int) bool {
	cp.mu.Lock()
	if slot < 0 || slot >= cp.size {
		cp.mu.Unlock()
		return false
	}
	for len(cp.pool) <= slot {
		cp.pool = append(cp.pool, credPoolEntry{})
	}
	if entryCredValid(cp.pool[slot]) {
		cp.mu.Unlock()
		return true
	}
	if cp.pool[slot].fetching {
		cp.mu.Unlock()
		return false
	}
	// Pre-fetch abort check (saves PoW). If conn-driven fetches in get()
	// have already pushed available past the cold-start target, this
	// fill is redundant.
	if abortIfAvailableGTE > 0 && cp.countAvailableLocked() >= abortIfAvailableGTE {
		cp.mu.Unlock()
		return false
	}
	// The dead-mint pause (credpool_relay.go): the grower, too, mints nothing
	// while the last mint named only relays marked unreachable — its reserve
	// fills would land the same dead pair and lengthen the pause.
	if cp.health.now().Before(cp.deadMintUntil) {
		cp.mu.Unlock()
		return false
	}
	gen := cp.claimForFetchLocked(slot)
	cp.mu.Unlock()

	fetchStart := time.Now()
	addr, creds, err := cp.fetch(allowCaptchaBlock, slot)

	cp.mu.Lock()
	if !cp.ownsLocked(slot, gen) {
		if err != nil {
			cp.mu.Unlock()
			log.Printf("credpool: background fill slot %d failed (%v) after the slot changed hands — nothing recorded", slot, err)
			return false
		}
		placed, why := cp.placeIntoEmptySlotLocked(addr, creds, 0)
		filled := cp.countFreshLocked()
		cp.mu.Unlock()
		if placed < 0 {
			log.Printf("credpool: slot %d changed hands during the background fetch — fetched cred dropped (%s)", slot, why)
			return false
		}
		log.Printf("credpool: slot %d changed hands during the background fetch — fetched cred placed into slot %d (%d/%d slots filled)", slot, placed, filled, cp.size)
		if placed < len(cp.authErrors) {
			cp.authErrors[placed].Store(0)
		}
		cp.saveToDisk()
		cp.broadcastSlotAvailable()
		return true
	}
	cp.pool[slot].fetching = false
	if err == nil {
		// No count-based checkpoint here: the fetch happened, the credential
		// is paid for, it goes into the slot whatever the count is now (see
		// the doc comment — the discard this replaced cost more than it
		// saved). The ownership check above is the only gate.
		// `active` from the record of leases still out on this (slot,
		// credential): 0 for a new identity — the old identity's sessions are
		// counted under THEIR key and their releases never reach this entry —
		// and the previous holders when the same credential is issued again.
		cp.placeLocked(slot, credPoolEntry{addr: addr, addrs: cp.candidatesForSlot(slot, creds), creds: creds, ts: time.Now(), active: cp.outstandingLocked(slot, creds), gen: gen})
		cp.noteVKAnsweredLocked(fetchStart)
		_ = cp.noteMintLocked(slot)
		filled := cp.countFreshLocked()
		cp.mu.Unlock()
		log.Printf("credpool: background filled slot %d (%d/%d slots filled)", slot, filled, cp.size)
		if slot >= 0 && slot < len(cp.authErrors) {
			cp.authErrors[slot].Store(0)
		}
		cp.saveToDisk()
		// Background grower just turned a non-fresh slot into a fresh
		// one. Wake conns parked after a cascade ForceReconnect — they
		// can now acquire the freshly-filled slot instead of waiting
		// out their randomised retry timer.
		cp.broadcastSlotAvailable()
		return true
	}
	cp.mu.Unlock()
	log.Printf("credpool: background fill slot %d failed: %v", slot, err)
	return false
}

// claimForFetchLocked marks slot as being fetched and returns the claim
// stamp the fetch must present when it publishes (ownsLocked). Caller holds
// cp.mu. Stamps are never reused, so a stamp taken before invalidate()
// rebuilt the pool can never match the zero entries it left.
func (cp *credPool) claimForFetchLocked(slot int) uint64 {
	cp.nextGen++
	cp.pool[slot].fetching = true
	cp.pool[slot].gen = cp.nextGen
	return cp.nextGen
}

// ownsLocked reports whether slot still carries the claim stamp gen, i.e.
// nothing has reset, cleared or re-claimed it since the fetch began. Caller
// holds cp.mu.
func (cp *credPool) ownsLocked(slot int, gen uint64) bool {
	return slot >= 0 && slot < len(cp.pool) && cp.pool[slot].gen == gen
}

// placeIntoEmptySlotLocked stores a credential whose original slot changed
// hands during its fetch into the first slot nobody holds (no credential, no
// fetch in flight) and returns that index — or -1 and the reason when it must
// not be kept, in which case the credential is dropped (one VK solve, nothing
// else). The entry gets its own fresh stamp. Caller holds cp.mu.
//
// 🚨 TWO REFUSALS (user's review, 2026-09-08 — reproduced 3/3: after ten
// leases on relay A the pool allowed an eleventh):
//
//  1. Cookie (VKAuth) mode: a slot IS a (call, relay) bucket — fetchFreshCreds
//     → cookieCredForSlot(slot) returns the SAME credential for the same slot
//     every time. After a reset the new fetch refills slot 0 with relay A's
//     credential; the late fetch returns that very credential and, moved into
//     the free slot 1 meant for relay B, would let the pool hand out ten more
//     allocations on A (VK's quota is per (identity, relay)) while counting
//     slot 1 as filled with B unused — 486s and an incomplete recovery. In
//     cookie mode a credential lives in its own slot or nowhere.
//  2. Any mode: a credential whose Username is already in the pool is a
//     duplicate of that slot (Username identifies the cred set — seedSlot's
//     dedup rule) and must not become a second copy of one quota.
//
// Anonymous credentials are unique per fetch, so relocation stays the normal
// outcome for them.
func (cp *credPool) placeIntoEmptySlotLocked(addr string, creds *TURNCreds, active int) (int, string) {
	if cookieAuthEnabled.Load() {
		return -1, "cookie mode: the credential is bound to its own slot's relay"
	}
	for len(cp.pool) < cp.size {
		cp.pool = append(cp.pool, credPoolEntry{})
	}
	if creds != nil {
		for i := range cp.pool {
			if cp.pool[i].creds != nil && cp.pool[i].creds.Username == creds.Username {
				return -1, fmt.Sprintf("duplicate of the credential already in slot %d", i)
			}
		}
	}
	for i := range cp.pool {
		if cp.pool[i].creds == nil && !cp.pool[i].fetching {
			cp.nextGen++
			cp.placeLocked(i, credPoolEntry{addr: addr, addrs: cp.candidatesForSlot(i, creds), creds: creds, ts: time.Now(), active: cp.outstandingLocked(i, creds), gen: cp.nextGen})
			for n := 0; n < active; n++ {
				cp.seatLocked(i, cp.dialNowLocked(i)) // the caller's own seat(s) — `active` of them
			}
			return i, ""
		}
	}
	return -1, "no slot is empty"
}

// pickSlotToFill selects the first missing or expiring slot without an active fetch.
func (cp *credPool) pickSlotToFill() int {
	cp.mu.Lock()
	defer cp.mu.Unlock()
	for len(cp.pool) < cp.size {
		cp.pool = append(cp.pool, credPoolEntry{})
	}
	for i := 0; i < cp.size; i++ {
		e := cp.pool[i]
		if cp.isFreshLocked(i) {
			continue // fresh (cred not yet near expiry)
		}
		if e.fetching {
			continue
		}
		return i
	}
	return -1
}

// invalidate drops every entry so the next get() refetches. Used on
// explicit session resets (Pause/Resume/ForceReconnect) and on captcha
// probes that need to force a fresh VK session.
func (cp *credPool) invalidate() {
	go cp.reresolveOverride("invalidate", false) // the credentials are gone; the list's names may point elsewhere by now (#105)
	cp.mu.Lock()
	n := 0
	for _, e := range cp.pool {
		if e.creds != nil {
			n++
		}
	}
	// Reset to pre-sized empty slice so subsequent ops (seedSlot
	// relocation, pickSlotToFill iteration) keep seeing the full
	// slot array; see newCredPool's pre-size comment.
	cp.pool = make([]credPoolEntry, cp.size)
	cp.mu.Unlock()
	if n > 0 {
		log.Printf("credpool: invalidated %d entries", n)
	}
}

// invalidateEntry drops one slot (by pool index, not necessarily a
// connIdx) so its cred is re-fetched on next need. Cooldown and
// fetching flags are cleared too.
//
// Callers should pass the slot that actually produced the bad cred —
// for conns that fell back via credPool.get fallback, that's the
// credSlot returned by get(), NOT the caller's connIdx.
//
// Persists the new pool state to disk so the next launch doesn't try
// to use this server-rejected cred (which would cost a 401 round-trip
// to discover the same thing). saveToDisk is a snapshot of current
// pool[].creds, so dropping the entry above is enough — no separate
// "delete from cache file" step.
//
// creds is the credential the relay rejected. A 401/403 for one the slot no
// longer holds must not drop what sits there now — another identity, freshly
// minted, with sessions of its own (holdsLocked; release has the full note).
func (cp *credPool) invalidateEntry(slot int, creds *TURNCreds) {
	if slot < 0 {
		return
	}
	cp.mu.Lock()
	dropped := cp.holdsLocked(slot, creds)
	stale := !dropped && slot < len(cp.pool) && cp.pool[slot].creds != nil
	if dropped {
		cp.pool[slot] = credPoolEntry{}
	}
	cp.mu.Unlock()
	if stale {
		log.Printf("credpool: an auth error on slot %d names a credential the slot no longer holds — the slot is kept (it was refilled since that lease)", slot)
	}
	if dropped {
		cp.saveToDisk()
	}
}

func (cp *credPool) broadcastSlotAvailable() {
	cp.mu.Lock()
	oldCh := cp.slotAvailableCh
	cp.slotAvailableCh = make(chan struct{})
	cp.mu.Unlock()
	close(oldCh)
}

// slotAvailableChannel returns the channel a conn should select on to
// receive the next slot-available broadcast. Must be re-read after each
// wake-up since broadcastSlotAvailable replaces the field. A conn that was
// just PARKED by get() does not read it: the park error carries the channel
// that was current under the lock (poolParkError) — see Proxy.wakeChannelFor.
func (cp *credPool) slotAvailableChannel() <-chan struct{} {
	cp.mu.Lock()
	defer cp.mu.Unlock()
	return cp.slotAvailableCh
}

// snapshotSize returns (freshCount, withCredsCount, totalCapacity) of
// the pool — used by the stats endpoint so the UI can display
// "fresh / with-creds / total". Takes the lock briefly; safe to call
// from any goroutine.
//
// fresh counts slots usable for NEW conn allocations (entryIsFresh).
// withCreds counts slots that physically hold a cred — including ones
// past their expiry buffer or in pending state — i.e. slots where
// existing conns may still be alive on a now-stale cred until their
// VK-side allocation lifetime runs out. The two diverge whenever a
// slot's username-encoded expiry is within 30 min: the slot drops out
// of "fresh" (UI shows lower number) while existing conns continue
// functioning until VK actually invalidates the allocation.
func (cp *credPool) snapshotSize() (available int, withUsableCreds int, size int) {
	cp.mu.Lock()
	// "Available" = fresh AND not currently saturated. The Stats UI
	// "Pool" first number used to count `fresh` (entryIsFresh) which
	// missed saturatedUntil — back-to-back path transitions on
	// build 72 (vpn.wifi-lte-wifi.1.log 2026-05-10 23:03) showed
	// "Pool 8/8/8" while ALL 8 slots were saturated for the next
	// 4 minutes. Switching to countAvailableLocked makes the UI
	// reflect actual usability.
	available = cp.countAvailableLocked()
	// "WithUsableCreds" = slots whose cred hasn't crossed the
	// expiry buffer. Replaces the prior countWithCredsLocked which
	// counted ANY cred regardless of expiry — misleading when
	// background grower stalls (PoW rate-limited) and slots hold
	// stale creds that can't be refreshed (vpn.wifi-lte-wifi.1.log
	// on 2026-05-12: UI showed "6/12/12" while 6 of those 12 slots
	// had expired creds the grower had been failing to refresh for
	// 26+ minutes). The middle number now matches the user's mental
	// model of "viable creds the pool can fall back on".
	withUsableCreds = cp.countWithUnexpiredCredsLocked()
	size = cp.size
	cp.mu.Unlock()
	return
}

// distinctRelays counts the number of DISTINCT TURN relay addresses currently
// held across the pool's filled slots — i.e. how many independent
// (per-(userid,relay)≈10) quota buckets the conns are spread over. Surfaced as
// the 4th "Pool" stat: the whole point in cookie/VKAuth mode, and harmless/
// informative in anonymous mode.
func (cp *credPool) distinctRelays() int {
	cp.mu.Lock()
	defer cp.mu.Unlock()
	seen := make(map[string]struct{})
	for i := range cp.pool {
		if cp.pool[i].creds != nil && cp.pool[i].addr != "" {
			seen[cp.pool[i].addr] = struct{}{}
		}
	}
	return len(seen)
}

// isFreshLocked checks credential validity. Caller holds cp.mu.
func (cp *credPool) isFreshLocked(slot int) bool {
	if slot < 0 || slot >= len(cp.pool) {
		return false
	}
	return cp.freshOnLocked(slot, cp.dialNowLocked(slot))
}

func (cp *credPool) availableLocked(slot int) bool {
	if !cp.isFreshLocked(slot) {
		return false
	}
	return !time.Now().Before(cp.markLocked(slot, cp.dialNowLocked(slot)))
}

// availableForLocked assumes cp.mu is held: the slot can seat THIS connection
// right now as far as the clock and the marks go — fresh on the relay the
// connection would dial (peekAddrLocked: its cursor's) and not benched THERE,
// with a relay to dial. The cold-start cap counts by it (countAvailableForLocked):
// a connection parks to share only a slot it could share. Room is not its
// question — a full slot is provisioned, the cap's own rule. The SLOT's own
// question (the grower, the UI, the logs) stays availableLocked, on the relay
// the slot dials now; the two parted with the per-relay marks of 2026-10-31
// (the stands, 2026-11-01: a slot benched on B and free on silent, unmarked A
// read "ready" to the cap and was refused to the connection on B — nothing
// minted, nothing answered, A never marked, 0/6 for two minutes). In cookie
// mode a slot dials one relay and the two are one.
func (cp *credPool) availableForLocked(slot, connIdx int) bool {
	if slot < 0 || slot >= len(cp.pool) || !cp.hasRelayLocked(slot) {
		return false
	}
	addr := cp.peekAddrLocked(slot, connIdx)
	if addr == "" || !cp.freshOnLocked(slot, addr) {
		return false
	}
	return !time.Now().Before(cp.markLocked(slot, addr))
}

// countAvailableForLocked assumes cp.mu is held: the slots that could seat
// connIdx now (availableForLocked) — the cold-start cap's count.
func (cp *credPool) countAvailableForLocked(connIdx int) int {
	n := 0
	for i := range cp.pool {
		if cp.availableForLocked(i, connIdx) {
			n++
		}
	}
	return n
}

// entryCredValid: the entry holds a credential still valid on the TURN
// server credExpiryBuffer from now — the part of "fresh" that is the
// credential's own, whatever relay the slot dials.
func entryCredValid(e credPoolEntry) bool {
	if e.creds == nil {
		return false
	}
	exp, ok := parseCredExpiry(e.creds.Username)
	if !ok {
		// Malformed username — treat as already expired so the next
		// fetch path replaces it. Safer than trusting an unparseable
		// cred indefinitely.
		return false
	}
	return time.Now().Add(credExpiryBuffer).Before(exp)
}

// countFreshLocked assumes cp.mu is held.
func (cp *credPool) countFreshLocked() int {
	n := 0
	for i := range cp.pool {
		if cp.isFreshLocked(i) {
			n++
		}
	}
	return n
}

// countAvailableLocked assumes cp.mu is held. Same as countFreshLocked
// but ALSO excludes saturated slots — see availableLocked. Used for the
// UI "available" count.
func (cp *credPool) countAvailableLocked() int {
	n := 0
	for i := range cp.pool {
		if cp.availableLocked(i) && cp.health.hasCandidate(cp.candidatesLocked(i)) {
			n++
		}
	}
	return n
}

func (cp *credPool) countWithUnexpiredCredsLocked() int {
	n := 0
	now := time.Now()
	for _, e := range cp.pool {
		if e.creds == nil {
			continue
		}
		exp, ok := parseCredExpiry(e.creds.Username)
		if !ok {
			continue
		}
		if !now.Add(credExpiryBuffer).Before(exp) {
			continue // expired or expiring within buffer
		}
		n++
	}
	return n
}

// logFreshTransitionsLocked reports credential validity changes between saves.
func (cp *credPool) logFreshTransitionsLocked() {
	// Resize snapshot on first run or after pool growth. On first run
	// we just snapshot current state without logging — there's no
	// "transition" to report when we have no history.
	if len(cp.freshLastTick) != len(cp.pool) {
		cp.freshLastTick = make([]bool, len(cp.pool))
		for i := range cp.pool {
			cp.freshLastTick[i] = cp.isFreshLocked(i)
		}
		return
	}
	now := time.Now()
	for slot := range cp.pool {
		wasFresh := cp.freshLastTick[slot]
		nowFresh := cp.isFreshLocked(slot)
		if wasFresh && !nowFresh {
			e := cp.pool[slot]
			reason := "unknown"
			switch {
			case e.creds == nil:
				reason = "cred removed"
			default:
				if exp, ok := parseCredExpiry(e.creds.Username); !ok {
					reason = "unparseable cred expiry"
				} else if !now.Add(credExpiryBuffer).Before(exp) {
					remaining := time.Until(exp).Round(time.Second)
					reason = fmt.Sprintf("cred expiring within %s buffer (cred expires in %s)",
						credExpiryBuffer, remaining)
				}
			}
			log.Printf("credpool: slot %d transitioned out of fresh — %s", slot, reason)
		} else if !wasFresh && nowFresh {
			e := cp.pool[slot]
			reason := "unknown"
			switch {
			case e.creds == nil:
				reason = "creds appeared (race; check seedSlot/background fill log)"
			default:
				reason = "creds became valid (seeded or background-filled)"
			}
			log.Printf("credpool: slot %d transitioned to fresh — %s", slot, reason)
		}
		cp.freshLastTick[slot] = nowFresh
	}
}

// countWithCredsLocked assumes cp.mu is held. Counts slots that hold a
// non-nil cred pointer regardless of expiry — i.e. the same predicate
// saveToDisk uses to decide what to persist. The Stats UI shows this
// alongside the fresh count so the user can tell apart "cred is gone"
// from "cred is still in the slot but past its expiry buffer / pending /
// saturated" — those latter cases keep existing conns alive on the slot
// even though pickSlotToFill / get's Phase 1 won't grant new ones.
func (cp *credPool) countWithCredsLocked() int {
	n := 0
	for _, e := range cp.pool {
		if e.creds != nil {
			n++
		}
	}
	return n
}

// fatalCallError inspects a VK API error object and returns a CallUnavailableError
// if the error_code denotes a NON-retryable call/link problem, else nil. VK uses
// different code spaces per API family, so we accept both:
//   - calls.getAnonymousToken (legacy path): the 9xxx range — 9000 "Call not
//     found", 9008 "Join link is not valid", … all call-scoped and non-retryable
//     (as opposed to 14 "Captcha need").
//   - messages.getCallPreview / getAnonymCallToken (VK Calls free path): the 95x
//     block — 951 "Call not found", 954 "Invalid join link". Caught here BEFORE
//     the legacy fallback so we never solve a captcha for a doomed call/link.
//
// error_msg is passed through as the user-facing Message.
func fatalCallError(resp map[string]interface{}) *CallUnavailableError {
	errObj, ok := resp["error"].(map[string]interface{})
	if !ok {
		return nil
	}
	codeF, _ := errObj["error_code"].(float64)
	code := int(codeF)
	switch {
	case code == 951, code == 954: // VK Calls free path: Call not found / Invalid join link
	case code >= 9000 && code <= 9999: // legacy calls.getAnonymousToken: 9xxx call-domain
	default:
		return nil
	}
	msg, _ := errObj["error_msg"].(string)
	return &CallUnavailableError{Code: code, Message: msg}
}

// extractCaptcha checks if a VK API response contains error code 14 (captcha required).
// Returns captcha_sid, captcha URL, captcha_ts, and captcha_attempt.
// Prefers redirect_uri (new interactive captcha) over captcha_img (deprecated text captcha).
func extractCaptcha(resp map[string]interface{}) (sid, captchaURL string, captchaTs, captchaAttempt float64) {
	errObj, ok := resp["error"].(map[string]interface{})
	if !ok {
		return "", "", 0, 0
	}
	code, _ := errObj["error_code"].(float64)
	if int(code) != 14 {
		return "", "", 0, 0
	}

	// Log the full error for debugging
	if errJSON, err := json.Marshal(errObj); err == nil {
		log.Printf("vk: captcha error response: %s", string(errJSON))
	}

	// Prefer redirect_uri (new "I'm not a robot" captcha that works in browser)
	if uri, ok := errObj["redirect_uri"].(string); ok && uri != "" {
		captchaURL = uri
	} else {
		// Fallback to old captcha_img
		captchaURL, _ = errObj["captcha_img"].(string)
	}

	// captcha_sid can be string or number
	switch v := errObj["captcha_sid"].(type) {
	case string:
		sid = v
	case float64:
		sid = fmt.Sprintf("%.0f", v)
	}

	// Extract captcha_ts and captcha_attempt for success_token retry
	captchaTs, _ = errObj["captcha_ts"].(float64)
	captchaAttempt, _ = errObj["captcha_attempt"].(float64)
	if captchaAttempt == 0 {
		captchaAttempt = 1
	}

	return sid, captchaURL, captchaTs, captchaAttempt
}

// nextEmptyShowHint moves the "VK has no slider ready" counter, and exists as
// its own function so the rule can be TESTED — it is a silent property, and it
// was silently wrong.
//
// 🚨 `showType` is only VK's answer when the attempt got far enough to be
// answered. On a page we could not PARSE, `htmlSettings` is never even
// extracted, so the empty string is OUR zero value. Counting it accused VK of a
// verdict it never gave, and skipped the third attempt on top.
//
// 🚨 AND THE FIRST FIX OF THIS WAS DEAD CODE: it keyed on `powErr == nil`, but
// EVERY non-success return of solveCaptchaPoW carries a non-nil error, so the
// incrementing branch could never run — correct in isolation, unreachable in
// situ, with a test passing on a state the program cannot produce.
// *(User-caught, twice; hence an explicit stage flag rather than a proxy for
// one.)*
//
//	!reached               → the attempt says NOTHING about VK's intent: hold
//	reached, show ""       → VK answered, and answered "no challenge": count it
//	show non-empty         → VK named a challenge: reset
func nextEmptyShowHint(reached bool, showType string, consecutive int) int {
	if showType != "" {
		return 0
	}
	if !reached {
		return consecutive
	}
	return consecutive + 1
}
