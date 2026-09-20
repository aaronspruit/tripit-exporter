package airtrail

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Client calls the AirTrail REST API with an API key.
type Client struct {
	// BaseURL is the root of the AirTrail instance, for example
	// "https://airtrail.example.com".
	BaseURL string
	// APIKey goes in the Authorization header of every request.
	APIKey string
	// HTTP is the client to use. A nil value means http.DefaultClient.
	HTTP *http.Client
	// Logf writes a progress line. A nil value writes nothing.
	Logf func(format string, args ...any)
	// Verbose writes one line for each request and its status. It never
	// writes the API key.
	Verbose bool
}

// AuthError means that AirTrail rejected the API key.
type AuthError struct{ Status int }

func (e *AuthError) Error() string {
	return fmt.Sprintf("AirTrail rejected the API key with status %d; make a new key in AirTrail under Settings", e.Status)
}

// FlightError means that AirTrail refused one flight. The run keeps going,
// because another flight can still succeed.
type FlightError struct {
	Status  int
	Message string
}

func (e *FlightError) Error() string {
	return fmt.Sprintf("AirTrail refused the flight with status %d: %s", e.Status, e.Message)
}

// NotFoundError means that the AirTrail flight of a stored id is gone,
// because a person removed it in AirTrail.
type NotFoundError struct{ ID int64 }

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("AirTrail holds no flight %d", e.ID)
}

// Save writes one flight. A flight with an id replaces the flight of that
// id, and a flight with no id is added. It returns the AirTrail flight id.
func (c *Client) Save(ctx context.Context, flight Flight) (int64, error) {
	var out struct {
		Success bool   `json:"success"`
		ID      int64  `json:"id"`
		Message string `json:"message"`
	}
	if err := c.post(ctx, "/api/flight/save", flight, &out); err != nil {
		return 0, err
	}
	if !out.Success {
		return 0, &FlightError{Status: http.StatusOK, Message: out.Message}
	}
	if out.ID != 0 {
		return out.ID, nil
	}
	// An update answers with no id, because the id did not change.
	return flight.ID, nil
}

// Delete removes the flight of id. A flight that AirTrail no longer holds
// returns a *NotFoundError, which the sync treats as done.
func (c *Client) Delete(ctx context.Context, id int64) error {
	var out struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	err := c.post(ctx, "/api/flight/delete", map[string]int64{"id": id}, &out)
	var flightErr *FlightError
	if errors.As(err, &flightErr) && strings.Contains(strings.ToLower(flightErr.Message), "not found") {
		return &NotFoundError{ID: id}
	}
	if err != nil {
		return err
	}
	if !out.Success {
		return &FlightError{Status: http.StatusOK, Message: out.Message}
	}
	return nil
}

// post sends body to path and reads the answer into out.
func (c *Client) post(ctx context.Context, path string, body, out any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("airtrail: encode the request for %s: %w", path, err)
	}
	url := strings.TrimSuffix(c.BaseURL, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("airtrail: build the request for %s: %w", path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.APIKey)

	client := c.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("airtrail: call %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	answer, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("airtrail: read the answer of %s: %w", path, err)
	}
	c.logf("%s answered %d", path, resp.StatusCode)

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return &AuthError{Status: resp.StatusCode}
	}
	if resp.StatusCode != http.StatusOK {
		return &FlightError{Status: resp.StatusCode, Message: errorMessage(answer)}
	}
	if err := json.Unmarshal(answer, out); err != nil {
		return fmt.Errorf("airtrail: read the answer of %s: %w", path, err)
	}
	return nil
}

// logf writes a progress line when the client is verbose.
func (c *Client) logf(format string, args ...any) {
	if !c.Verbose || c.Logf == nil {
		return
	}
	c.Logf(format, args...)
}

// errorMessage reads the message of a failed answer. AirTrail writes either
// a message string or the issue list of its schema check.
func errorMessage(body []byte) string {
	var answer struct {
		Message string `json:"message"`
		Errors  []struct {
			Path    []any  `json:"path"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(body, &answer); err != nil {
		return strings.TrimSpace(string(body))
	}
	if answer.Message != "" {
		return answer.Message
	}
	var parts []string
	for _, e := range answer.Errors {
		if len(e.Path) > 0 {
			parts = append(parts, fmt.Sprintf("%v: %s", e.Path[len(e.Path)-1], e.Message))
			continue
		}
		parts = append(parts, e.Message)
	}
	if len(parts) == 0 {
		return strings.TrimSpace(string(body))
	}
	return strings.Join(parts, ", ")
}
