// Package feed fetches the TripIt private calendar feed and maps each HTTP
// result to an error type that the caller turns into an exit code.
package feed

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Timeout is the time the fetch waits for the server to respond.
const Timeout = 60 * time.Second

// CredentialError means the server rejected the feed URL: it is wrong or
// revoked, and a person must copy a new one from TripIt.
type CredentialError struct {
	StatusCode int
}

func (e *CredentialError) Error() string {
	return fmt.Sprintf("feed: the server rejected the feed URL with status %d", e.StatusCode)
}

// Fetch fetches the calendar feed at feedURL. rateLimited reports a 429
// response: it is not an error, the archive changes nothing, and the next
// run continues. Fetch never puts feedURL into the text of an error, because
// the URL holds the feed credential.
func Fetch(ctx context.Context, client *http.Client, feedURL string) (calendar []byte, rateLimited bool, err error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, feedURL, nil)
	if err != nil {
		return nil, false, redact(err, feedURL)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, false, redact(err, feedURL)
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusGone:
		return nil, false, &CredentialError{StatusCode: resp.StatusCode}
	case http.StatusTooManyRequests:
		return nil, true, nil
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, false, redact(err, feedURL)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("feed: unexpected status %d", resp.StatusCode)
	}

	trimmed := strings.TrimSpace(string(body))
	if !strings.HasPrefix(trimmed, "BEGIN:VCALENDAR") || !strings.HasSuffix(trimmed, "END:VCALENDAR") {
		return nil, false, errors.New("feed: response body is not a calendar")
	}

	return body, false, nil
}

// ExitCode returns the exit code for the error that Fetch returned. err is
// nil for both a successful fetch and a 429 rate limit.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var credErr *CredentialError
	if errors.As(err, &credErr) {
		return 1
	}
	return 2
}

// redact replaces feedURL in err's message, so a network error from
// net/http, which quotes the full request URL, never logs the credential.
func redact(err error, feedURL string) error {
	return errors.New(strings.ReplaceAll(err.Error(), feedURL, "<feed URL redacted>"))
}
