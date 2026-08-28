package protonapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// These tests pin do()'s failure handling: the error envelope decode, the
// raw-body snippet when the envelope is absent, the 429/503 retry, and the
// one-shot 401 refresh hook. All of them run against a local httptest server;
// none touch the live API.

func newTestClient(url string) *Client {
	return New(url, "test-bridge@1.0.0", "test-uid", "test-token")
}

func TestDoDecodesAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		fmt.Fprint(w, `{"Code": 9001, "Error": "human verification required"}`)
	}))
	defer srv.Close()

	err := newTestClient(srv.URL).do(context.Background(), http.MethodGet, "/test", nil, nil, nil)

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is %T, want *APIError: %v", err, err)
	}

	if apiErr.Status != 422 || apiErr.Code != 9001 || apiErr.Message != "human verification required" {
		t.Errorf("decoded %+v, want status 422 code 9001 with message", apiErr)
	}
}

func TestDoNonJSONBodySnippet(t *testing.T) {
	long := "<html>" + strings.Repeat("gateway exploded ", 30) + "</html>"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprint(w, long)
	}))
	defer srv.Close()

	err := newTestClient(srv.URL).do(context.Background(), http.MethodGet, "/test", nil, nil, nil)

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is %T, want *APIError: %v", err, err)
	}

	if !strings.Contains(err.Error(), "<html>gateway exploded") {
		t.Errorf("error %q does not carry the body snippet", err)
	}

	if len(apiErr.Snippet) > snippetLen+len("...") {
		t.Errorf("snippet is %d bytes, want at most %d plus ellipsis", len(apiErr.Snippet), snippetLen)
	}
}

func TestDoRetriesOn429(t *testing.T) {
	var calls atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)

			return
		}

		fmt.Fprint(w, `{"Code": 1000}`)
	}))
	defer srv.Close()

	if err := newTestClient(srv.URL).do(context.Background(), http.MethodGet, "/test", nil, nil, nil); err != nil {
		t.Fatalf("do after one 429: %v", err)
	}

	if got := calls.Load(); got != 2 {
		t.Errorf("server saw %d calls, want 2", got)
	}
}

func TestDoGivesUpAfterRetryCap(t *testing.T) {
	var calls atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	err := newTestClient(srv.URL).do(context.Background(), http.MethodGet, "/test", nil, nil, nil)

	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 503 {
		t.Fatalf("error is %v, want *APIError with status 503", err)
	}

	if got := calls.Load(); got != int32(1+maxRetries) {
		t.Errorf("server saw %d calls, want %d", got, 1+maxRetries)
	}
}

func TestDoRetryWaitRespectsContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "5")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := newTestClient(srv.URL).do(ctx, http.MethodGet, "/test", nil, nil, nil)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error is %v, want context.DeadlineExceeded", err)
	}

	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("cancellation took %v, want well under the 5s Retry-After", elapsed)
	}
}

func TestDoRefreshesOnceOn401(t *testing.T) {
	var calls atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)

		if r.Header.Get("Authorization") != "Bearer fresh-token" {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"Code": 401, "Error": "Invalid access token"}`)

			return
		}

		fmt.Fprint(w, `{"Code": 1000}`)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)

	var refreshes atomic.Int32

	c.SetRefreshFunc(func(context.Context) error {
		refreshes.Add(1)
		// Simulates the upstream auth handler pushing the rotated token in.
		c.SetAuth("fresh-uid", "fresh-token")

		return nil
	})

	if err := c.do(context.Background(), http.MethodGet, "/test", nil, nil, nil); err != nil {
		t.Fatalf("do after refresh: %v", err)
	}

	if got := refreshes.Load(); got != 1 {
		t.Errorf("refresh ran %d times, want 1", got)
	}

	if got := calls.Load(); got != 2 {
		t.Errorf("server saw %d calls, want 2", got)
	}
}

func TestDoRefreshDoesNotLoop(t *testing.T) {
	// A refresh that "succeeds" without actually fixing the token must not
	// retry forever: one refresh, one retry, then the 401 surfaces.
	var calls atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"Code": 401, "Error": "Invalid access token"}`)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)

	var refreshes atomic.Int32

	c.SetRefreshFunc(func(context.Context) error {
		refreshes.Add(1)
		return nil
	})

	err := c.do(context.Background(), http.MethodGet, "/test", nil, nil, nil)

	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 401 {
		t.Fatalf("error is %v, want *APIError with status 401", err)
	}

	if got := refreshes.Load(); got != 1 {
		t.Errorf("refresh ran %d times, want 1", got)
	}

	if got := calls.Load(); got != 2 {
		t.Errorf("server saw %d calls, want 2", got)
	}
}

func TestEventsTruncated(t *testing.T) {
	var calls atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"Code": 1000, "EventID": "evt-%d", "More": 1}`, calls.Add(1))
	}))
	defer srv.Close()

	events, err := newTestClient(srv.URL).Events(context.Background(), "evt-0")

	if !errors.Is(err, ErrTruncated) {
		t.Fatalf("error is %v, want ErrTruncated", err)
	}

	if len(events) != maxWalkPages {
		t.Errorf("got %d events alongside the error, want %d", len(events), maxWalkPages)
	}
}

func TestConversationsReturnsTotal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"Code": 1000, "Total": 42, "Conversations": [{"ID": "c1"}]}`)
	}))
	defer srv.Close()

	convs, total, err := newTestClient(srv.URL).Conversations(context.Background(), 0, 50, ConversationFilter{})
	if err != nil {
		t.Fatalf("conversations: %v", err)
	}

	if total != 42 || len(convs) != 1 {
		t.Errorf("got %d conversations with total %d, want 1 and 42", len(convs), total)
	}
}

func TestRetryDelay(t *testing.T) {
	for _, tc := range []struct {
		name    string
		header  string
		attempt int
		want    time.Duration
	}{
		{"seconds", "2", 1, 2 * time.Second},
		{"seconds capped", "600", 1, maxRetryAfter},
		{"absent grows with attempt", "", 2, 2 * time.Second},
		{"garbage falls back", "soon", 1, time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := retryDelay(tc.header, tc.attempt); got != tc.want {
				t.Errorf("retryDelay(%q, %d) = %v, want %v", tc.header, tc.attempt, got, tc.want)
			}
		})
	}
}
