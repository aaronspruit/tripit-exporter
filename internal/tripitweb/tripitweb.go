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
	"strconv"
	"strings"
	"time"
)

// DefaultBaseURL is the TripIt host. A test overrides Client.BaseURL with a
// fake server URL.
const DefaultBaseURL = "https://www.tripit.com"

// pace is the wait between two trip requests, so the backfill does not
// trigger the rate limit that docs/research.md reports.
const pace = 1 * time.Second

// retryDelay is the wait before the one retry of a 401 response.
const retryDelay = 5 * time.Second

// requestTimeout is the time that one request waits for the full response,
// so a stalled connection stops the run instead of hanging it. A test sets
// a shorter value.
var requestTimeout = 60 * time.Second

// AuthError means the server rejected the session cookie twice: once, and
// again after the one retry. A person must copy a new cookie.
type AuthError struct{}

func (e *AuthError) Error() string {
	return "tripitweb: the session cookie was rejected twice, copy a new one"
}

// RateLimitedError means the server sent 429, or the connection failed with
// an HTTP/2 protocol error. The run stops here; the next run continues from
// the trips it already wrote.
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
	// Cookie is the session cookie. The client sends it as the Cookie
	// header of every request, and never writes it anywhere else.
	Cookie string
	// Sleep replaces time.Sleep in a test, so a test never waits for real.
	Sleep func(time.Duration)
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

// Pace waits the pace duration. The caller calls it between two requests
// that it sends one after the other, for example before each trip. ListTrips
// calls it between two pages.
func (c *Client) Pace() { c.sleep(pace) }

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
		if page > 1 {
			c.Pace()
		}
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
// to calendar" download URL needs beyond the cookie, as docs/research.md
// describes. It is a placeholder set, built from a typical Firefox request,
// because the live test against the real account that would confirm the
// exact required subset has not run yet; see docs/research.md open
// questions 1 and 4.
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
// open question 4 has not confirmed whether TripIt reads it.
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

// get sends one GET with the cookie and headers, and returns the body and
// the status code. A network error that looks like an HTTP/2 protocol error
// becomes a *RateLimitedError, because TripIt has been seen to end a long
// backfill run that way.
func (c *Client) get(ctx context.Context, url string, headers map[string]string) ([]byte, int, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Cookie", c.Cookie)
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if isProtocolError(err) {
			return nil, 0, &RateLimitedError{cause: err}
		}
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return body, resp.StatusCode, nil
}

// isProtocolError reports whether err looks like the ERR_HTTP2_PROTOCOL_ERROR
// that docs/research.md records after a long run of requests.
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
