// Package tripitweb is a client for the undocumented TripIt web API v2 and
// the "Export trip to calendar" download URL. It needs a session cookie: a
// person copies it from a browser, and the client never writes it to a file
// or a log.
package tripitweb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// DefaultBaseURL is the TripIt host. A test overrides Client.BaseURL with a
// fake server URL.
const DefaultBaseURL = "https://www.tripit.com"

// pace is the wait before each request after the first. TripIt reset a
// real backfill run after about 12 requests in 10 seconds, as
// docs/research.md records.
const pace = 5 * time.Second

// throttleWaits are the waits before each retry of a request that TripIt
// throttled: a 429, a TCP reset, an HTTP/2 protocol error, or a request that
// timed out. After the last retry, the request returns a *RateLimitedError.
var throttleWaits = []time.Duration{1 * time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute}

// retryDelay is the wait before the one retry of a 401 response.
const retryDelay = 5 * time.Second

// requestTimeout is the time that one request waits for the full response.
// A normal response takes a few seconds, and TripIt throttles by holding a
// request with no response, so a short timeout wastes less time.
// A stalled request counts as throttled, so it gets the retries of
// throttleWaits. A test sets a shorter value.
var requestTimeout = 20 * time.Second

// AuthError means the server rejected the session cookie twice: once, and
// again after the one retry. A person must copy a new cookie.
type AuthError struct{}

func (e *AuthError) Error() string {
	return "tripitweb: the session cookie was rejected twice, copy a new one"
}

// RateLimitedError means that TripIt still throttled a request after every
// wait of throttleWaits. The run stops here; the next run continues from the
// trips it already wrote.
type RateLimitedError struct{ cause error }

func (e *RateLimitedError) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("tripitweb: rate-limited: %v", e.cause)
	}
	return "tripitweb: rate-limited"
}

func (e *RateLimitedError) Unwrap() error { return e.cause }

// DownloadError means the download URL returned a status other than 200 or
// 429 for one trip, for example the 403 that TripIt sends when it blocks a
// client. It stops that trip, and not the run.
type DownloadError struct {
	UUID   string
	Status int
}

func (e *DownloadError) Error() string {
	return fmt.Sprintf("tripitweb: download trip %s: unexpected status %d", e.UUID, e.Status)
}

// Client calls the TripIt web API v2 and the download URL.
type Client struct {
	// BaseURL replaces DefaultBaseURL in a test.
	BaseURL string
	// Cookie is the session cookie, as the Cookie header of a browser
	// request. The client applies each Set-Cookie of a response to it, as a
	// browser does, because TripIt updates its Akamai cookies on each
	// response. The client sends it as the Cookie
	// header of every request, and never writes it anywhere else.
	Cookie string
	// Sleep replaces time.Sleep in a test, so a test never waits for real.
	Sleep func(time.Duration)
	// Logf, when it is not nil, gets one line before each wait for a
	// throttled request, so an operator sees why the run is slow.
	Logf func(format string, args ...any)
	// Verbose makes the client write each request and each response through
	// Logf. The lines hide the value of the cookie and of each Set-Cookie
	// header.
	Verbose bool

	sent    bool
	cookies []cookiePair
	parsed  bool
}

// cookiePair is one name=value item of the Cookie header. An item with no
// "=" has an empty name, and the client sends its value as it is.
type cookiePair struct{ name, value string }

// cookieHeader returns the Cookie header for the next request: the pasted
// cookie, with each update of a Set-Cookie header applied.
func (c *Client) cookieHeader() string {
	if !c.parsed {
		c.parsed = true
		for _, item := range strings.Split(c.Cookie, ";") {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			name, value, ok := strings.Cut(item, "=")
			if !ok {
				name, value = "", item
			}
			c.cookies = append(c.cookies, cookiePair{name: name, value: value})
		}
	}
	items := make([]string, len(c.cookies))
	for i, p := range c.cookies {
		if p.name == "" {
			items[i] = p.value
			continue
		}
		items[i] = p.name + "=" + p.value
	}
	return strings.Join(items, "; ")
}

// applySetCookies updates the cookie with each Set-Cookie header of resp.
// A cookie that the server expires is removed.
func (c *Client) applySetCookies(resp *http.Response) {
	for _, set := range resp.Cookies() {
		expired := set.MaxAge < 0 || (!set.Expires.IsZero() && set.Expires.Before(time.Now()))
		i := slices.IndexFunc(c.cookies, func(p cookiePair) bool { return p.name == set.Name })
		switch {
		case expired && i >= 0:
			c.cookies = slices.Delete(c.cookies, i, i+1)
		case expired:
		case i >= 0:
			c.cookies[i].value = set.Value
		default:
			c.cookies = append(c.cookies, cookiePair{name: set.Name, value: set.Value})
		}
	}
}

func (c *Client) baseURL() string {
	if c.BaseURL != "" {
		return c.BaseURL
	}
	return DefaultBaseURL
}

func (c *Client) sleep(d time.Duration) {
	if c.Sleep != nil {
		c.Sleep(d)
		return
	}
	time.Sleep(d)
}

// Profile calls /api/v2/get/profile. It returns an *AuthError when the
// cookie is invalid, before the caller makes any trip request.
func (c *Client) Profile(ctx context.Context) error {
	_, err := c.apiGet(ctx, "/api/v2/get/profile")
	return err
}

// ListTrips lists every trip that query matches, reading each page up to
// max_page. query holds the TripIt parameters other than page_size and
// page_num, for example "exclude_types=weather&past=true&traveler=all".
func (c *Client) ListTrips(ctx context.Context, query string) ([]json.RawMessage, error) {
	const pageSize = 50

	var all []json.RawMessage
	for page := 1; ; page++ {
		path := fmt.Sprintf("/api/v2/list/trip?%s&page_size=%d&page_num=%d", query, pageSize, page)
		body, err := c.apiGet(ctx, path)
		if err != nil {
			return nil, err
		}

		var resp struct {
			MaxPage json.RawMessage `json:"max_page"`
			Trip    json.RawMessage `json:"Trip"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("tripitweb: parse trip list: %w", err)
		}
		all = append(all, NormalizeList(resp.Trip)...)

		maxPage, err := intField(resp.MaxPage)
		if err != nil || page >= maxPage {
			break
		}
	}
	return all, nil
}

// GetTripDetail calls /api/v2/get/trip/uuid/<uuid>, and returns the response
// body exactly as it read it.
func (c *Client) GetTripDetail(ctx context.Context, uuid string) (json.RawMessage, error) {
	path := fmt.Sprintf("/api/v2/get/trip/uuid/%s/include_objects/true?exclude_types=weather", uuid)
	body, err := c.apiGet(ctx, path)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(body), nil
}

// apiHeaders are the two headers that a web API v2 route needs beyond the
// cookie. Without X-Requested-With, TripIt returns 401 for a valid session.
var apiHeaders = map[string]string{
	"Accept":           "application/json",
	"X-Requested-With": "XMLHttpRequest",
}

// apiGet sends one GET to a web API v2 route, and applies the retry rule: a
// 401 gets one retry after 5 seconds, and a second 401 becomes an
// *AuthError. A 429, on either try, becomes a *RateLimitedError.
func (c *Client) apiGet(ctx context.Context, path string) ([]byte, error) {
	body, status, err := c.get(ctx, c.baseURL()+path, apiHeaders)
	if err != nil {
		return nil, err
	}
	if status == http.StatusTooManyRequests {
		return nil, &RateLimitedError{}
	}
	if status != http.StatusUnauthorized {
		if status != http.StatusOK {
			return nil, fmt.Errorf("tripitweb: unexpected status %d for %s", status, path)
		}
		return body, nil
	}

	c.sleep(retryDelay)
	body, status, err = c.get(ctx, c.baseURL()+path, apiHeaders)
	if err != nil {
		return nil, err
	}
	switch status {
	case http.StatusTooManyRequests:
		return nil, &RateLimitedError{}
	case http.StatusUnauthorized:
		return nil, &AuthError{}
	case http.StatusOK:
		return body, nil
	default:
		return nil, fmt.Errorf("tripitweb: unexpected status %d for %s", status, path)
	}
}

// downloadHeaders is the fixed set of browser headers that the "Export trip
// to calendar" download URL needs beyond the cookie, built from a typical
// Firefox request. docs/research.md open question 1 records what the real
// account confirmed about this set.
var downloadHeaders = map[string]string{
	"User-Agent":                "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:128.0) Gecko/20100101 Firefox/128.0",
	"Accept":                    "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
	"Accept-Language":           "en-US,en;q=0.5",
	"Referer":                   DefaultBaseURL + "/",
	"Upgrade-Insecure-Requests": "1",
	"Sec-Fetch-Dest":            "document",
	"Sec-Fetch-Mode":            "navigate",
	"Sec-Fetch-Site":            "same-origin",
	"Sec-Fetch-User":            "?1",
}

// DownloadICS downloads the events of one trip from "Export trip to
// calendar". name is the file name at the end of the URL; docs/research.md
// open question 2 records what is known about it.
func (c *Client) DownloadICS(ctx context.Context, uuid, name string) ([]byte, error) {
	url := fmt.Sprintf("%s/trip/download/uuid/%s/%s", c.baseURL(), uuid, name)
	body, status, err := c.get(ctx, url, downloadHeaders)
	if err != nil {
		return nil, err
	}
	switch status {
	case http.StatusTooManyRequests:
		return nil, &RateLimitedError{}
	case http.StatusOK:
		return body, nil
	default:
		return nil, &DownloadError{UUID: uuid, Status: status}
	}
}

// get sends one GET with the cookie and headers after the pace wait, and
// returns the body and the status code. When TripIt throttles the request,
// get waits each duration of throttleWaits in turn and sends it again.
func (c *Client) get(ctx context.Context, url string, headers map[string]string) ([]byte, int, error) {
	for attempt := 0; ; attempt++ {
		if c.sent {
			c.sleep(pace)
		}
		c.sent = true

		body, status, err := c.send(ctx, url, headers)
		if !isThrottled(status, err) {
			return body, status, err
		}
		if attempt == len(throttleWaits) {
			if err != nil {
				return nil, 0, &RateLimitedError{cause: err}
			}
			return body, status, nil
		}

		reason := fmt.Sprintf("status %d", status)
		if err != nil {
			reason = err.Error()
		}
		if c.Logf != nil {
			c.Logf("TripIt throttled a request (%s); waiting %s, then trying again", reason, throttleWaits[attempt])
		}
		c.sleep(throttleWaits[attempt])
	}
}

// send sends one GET with the cookie and headers, with no pace and no retry.
func (c *Client) send(ctx context.Context, url string, headers map[string]string) ([]byte, int, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Cookie", c.cookieHeader())
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	c.logRequest(req)
	start := time.Now()

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.verbosef("<- error after %s: %v", time.Since(start).Round(time.Millisecond), err)
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	c.applySetCookies(resp)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		c.verbosef("<- %s %d, body read error after %s: %v", resp.Proto, resp.StatusCode, time.Since(start).Round(time.Millisecond), err)
		return nil, resp.StatusCode, err
	}
	c.logResponse(resp, body, time.Since(start))
	return body, resp.StatusCode, nil
}

// verbosePreview is the number of body bytes that verbose mode shows for a
// response other than 200.
const verbosePreview = 300

func (c *Client) verbosef(format string, args ...any) {
	if c.Verbose && c.Logf != nil {
		c.Logf("%s "+format, append([]any{time.Now().Format("15:04:05.000")}, args...)...)
	}
}

func (c *Client) logRequest(req *http.Request) {
	if !c.Verbose || c.Logf == nil {
		return
	}
	c.verbosef("-> %s %s", req.Method, req.URL)
	for _, name := range sortedKeys(req.Header) {
		value := strings.Join(req.Header[name], ", ")
		if name == "Cookie" {
			value = fmt.Sprintf("<hidden, %d bytes>", len(value))
		}
		c.verbosef("   %s: %s", name, value)
	}
}

func (c *Client) logResponse(resp *http.Response, body []byte, elapsed time.Duration) {
	if !c.Verbose || c.Logf == nil {
		return
	}
	c.verbosef("<- %s %d in %s, %d bytes", resp.Proto, resp.StatusCode, elapsed.Round(time.Millisecond), len(body))
	for _, name := range sortedKeys(resp.Header) {
		values := resp.Header[name]
		if name == "Set-Cookie" {
			values = cookieNames(values)
		}
		c.verbosef("   %s: %s", name, strings.Join(values, ", "))
	}
	if resp.StatusCode != http.StatusOK {
		preview := body
		if len(preview) > verbosePreview {
			preview = preview[:verbosePreview]
		}
		c.verbosef("   body: %q", preview)
	}
}

func sortedKeys(h http.Header) []string {
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// cookieNames replaces the value of each Set-Cookie header with <hidden>,
// and keeps the cookie name.
func cookieNames(values []string) []string {
	out := make([]string, len(values))
	for i, v := range values {
		name, _, _ := strings.Cut(v, "=")
		out[i] = name + "=<hidden>"
	}
	return out
}

// isThrottled reports whether TripIt throttled a request. docs/research.md
// records each form: a 429, the ERR_HTTP2_PROTOCOL_ERROR of a long run, and
// the TCP reset and the stalled request of a real backfill run.
func isThrottled(status int, err error) bool {
	if err == nil {
		return status == http.StatusTooManyRequests
	}
	return isProtocolError(err) || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, context.DeadlineExceeded)
}

func isProtocolError(err error) bool {
	return strings.Contains(strings.ToUpper(err.Error()), "PROTOCOL_ERROR")
}

// intField parses a JSON number or a quoted numeric string, because
// TripIt's XML-to-JSON conversion is not consistent about which one it
// sends.
func intField(raw json.RawMessage) (int, error) {
	s := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	return strconv.Atoi(s)
}

// NormalizeList turns a JSON value that is a single object into an array of
// one. TripIt's XML-to-JSON conversion collapses a one-item list to a bare
// object, so every plan list goes through this before the caller ranges
// over it.
func NormalizeList(raw json.RawMessage) []json.RawMessage {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil
	}
	if trimmed[0] != '[' {
		return []json.RawMessage{json.RawMessage(trimmed)}
	}

	var arr []json.RawMessage
	if err := json.Unmarshal(trimmed, &arr); err != nil {
		return nil
	}
	return arr
}

// DedupeByUUID removes a duplicate object by its "uuid" field, and keeps
// the first occurrence. traveler=all returns a plan twice when TripIt
// shares its trip with another traveler.
func DedupeByUUID(items []json.RawMessage) []json.RawMessage {
	seen := make(map[string]bool, len(items))
	out := make([]json.RawMessage, 0, len(items))
	for _, item := range items {
		uuid := UUIDField(item)
		if uuid != "" {
			if seen[uuid] {
				continue
			}
			seen[uuid] = true
		}
		out = append(out, item)
	}
	return out
}

// UUIDField reads the "uuid" field of a JSON object, and returns "" when
// the object has none.
func UUIDField(raw json.RawMessage) string {
	var v struct {
		UUID string `json:"uuid"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return ""
	}
	return v.UUID
}

// ExitCode returns the exit code for the error that a client call returned.
// err is nil for success.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var authErr *AuthError
	if errors.As(err, &authErr) {
		return 1
	}
	var rateLimitErr *RateLimitedError
	if errors.As(err, &rateLimitErr) {
		return 0
	}
	return 2
}
