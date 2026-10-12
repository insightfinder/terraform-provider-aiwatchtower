// Copyright (c) InsightFinder Inc.
// SPDX-License-Identifier: Apache-2.0

package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func newTestClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	c, err := NewClient(srv.URL, "test-user", "test-key")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	c.RetryBaseDelay = time.Millisecond
	return c
}

func TestNewClientRejectsEmptyArguments(t *testing.T) {
	for _, tc := range []struct{ name, base, user, key string }{
		{"no base URL", "", "u", "k"},
		{"no username", "http://x", "", "k"},
		{"no api key", "http://x", "u", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewClient(tc.base, tc.user, tc.key); err == nil {
				t.Fatal("expected an error, got nil")
			}
		})
	}
}

func TestDoRequestSendsAuthHeaders(t *testing.T) {
	var gotUser, gotKey, gotAccept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser = r.Header.Get("X-User-Name")
		gotKey = r.Header.Get("X-Api-Key")
		gotAccept = r.Header.Get("Accept")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	if _, _, err := c.DoRequest(context.Background(), http.MethodGet, "/api/external/v1/model", nil); err != nil {
		t.Fatalf("DoRequest: %v", err)
	}

	if gotUser != "test-user" {
		t.Errorf("X-User-Name = %q, want %q", gotUser, "test-user")
	}
	if gotKey != "test-key" {
		t.Errorf("X-Api-Key = %q, want %q", gotKey, "test-key")
	}
	if gotAccept != "application/json" {
		t.Errorf("Accept = %q, want %q", gotAccept, "application/json")
	}
}

func TestDoRequestRetriesIdempotentOnServerError(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	_, status, err := c.DoRequest(context.Background(), http.MethodGet, "/api/external/v1/model", nil)
	if err != nil {
		t.Fatalf("DoRequest: %v", err)
	}
	if status != http.StatusOK {
		t.Errorf("status = %d, want 200", status)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Errorf("server was called %d times, want 3 (two failures then a success)", got)
	}
}

func TestDoRequestStopsAfterMaxRetries(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	_, status, err := c.DoRequest(context.Background(), http.MethodGet, "/api/external/v1/model", nil)

	if err != nil {
		t.Fatalf("DoRequest returned an error instead of the final response: %v", err)
	}
	if status != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", status)
	}
	want := int32(DefaultMaxRetries + 1)
	if got := atomic.LoadInt32(&calls); got != want {
		t.Errorf("server was called %d times, want %d (one attempt plus %d retries)", got, want, DefaultMaxRetries)
	}
}

func TestDoRequestDoesNotRetryPostOnServerError(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	if _, _, err := c.DoRequest(context.Background(), http.MethodPost, "/api/external/v1/model", map[string]string{"a": "b"}); err != nil {
		t.Fatalf("DoRequest: %v", err)
	}

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("POST was sent %d times, want 1", got)
	}
}

func TestDoRequestDoesNotRetryClientError(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	if _, _, err := c.DoRequest(context.Background(), http.MethodGet, "/api/external/v1/model", nil); err != nil {
		t.Fatalf("DoRequest: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("a 400 was retried %d times; deterministic rejections must not be retried", got-1)
	}
}

func TestDoRequestRetriesOn429(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	_, status, err := c.DoRequest(context.Background(), http.MethodGet, "/api/external/v1/model", nil)
	if err != nil || status != http.StatusOK {
		t.Fatalf("status=%d err=%v, want 200 and no error", status, err)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Errorf("server was called %d times, want 2", got)
	}
}

func TestDoRequestHonoursCancelledContext(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	c.RetryBaseDelay = 500 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	if _, _, err := c.DoRequest(ctx, http.MethodGet, "/api/external/v1/model", nil); err == nil {
		t.Fatal("expected an error once the context expired")
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("server was called %d times after the context expired, want 1", got)
	}
}

func TestDoRequestPerAttemptTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	c.Timeout = 20 * time.Millisecond
	c.MaxRetries = 0

	start := time.Now()
	_, _, err := c.DoRequest(context.Background(), http.MethodGet, "/api/external/v1/model", nil)
	if err == nil {
		t.Fatal("expected a timeout error")
	}
	if elapsed := time.Since(start); elapsed > 150*time.Millisecond {
		t.Errorf("attempt took %s; the per-attempt timeout did not fire", elapsed)
	}
}

func TestDoRequestRetriesTransportFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()

	c, err := NewClient(url, "u", "k")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	c.RetryBaseDelay = time.Millisecond

	start := time.Now()
	if _, _, err := c.DoRequest(context.Background(), http.MethodGet, "/x", nil); err == nil {
		t.Fatal("expected a transport error")
	}
	if elapsed := time.Since(start); elapsed < 3*time.Millisecond {
		t.Errorf("returned after %s; the transport failure does not appear to have been retried", elapsed)
	}
}

func TestDoRequestDoesNotLeakCredentialsInErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	addr := srv.URL
	srv.Close()

	c, err := NewClient(addr, "secret-user", "secret-key")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	c.RetryBaseDelay = time.Millisecond

	_, err = c.GetModel(context.Background(), "secret-user", "a-model", "V1")
	if err == nil {
		t.Fatal("expected a transport error")
	}

	msg := err.Error()
	if strings.Contains(msg, "secret-user") {
		t.Errorf("error leaks the user name %d time(s): %s", strings.Count(msg, "secret-user"), msg)
	}
	if strings.Contains(msg, "secret-key") {
		t.Errorf("error leaks the API key: %s", msg)
	}
	if !strings.Contains(msg, "GET") || !strings.Contains(msg, "/api/external/v1/model") {
		t.Errorf("error no longer identifies the failing request: %s", msg)
	}
}

func TestNewClientNormalisesBaseURL(t *testing.T) {
	for _, in := range []string{
		"https://ai.insightfinder.com/",
		"https://ai.insightfinder.com///",
		"  https://ai.insightfinder.com  ",
	} {
		c, err := NewClient(in, "u", "k")
		if err != nil {
			t.Fatalf("NewClient(%q): %v", in, err)
		}
		if c.BaseURL != "https://ai.insightfinder.com" {
			t.Errorf("NewClient(%q).BaseURL = %q", in, c.BaseURL)
		}
	}

	if _, err := NewClient("///", "u", "k"); err == nil {
		t.Error("expected an error for a base URL consisting only of slashes")
	}
}

func TestDoRequestBuildsCleanURL(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c, err := NewClient(srv.URL+"/", "u", "k")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	c.RetryBaseDelay = time.Millisecond

	if _, _, err := c.DoRequest(context.Background(), http.MethodGet, "/api/external/v1/model", nil); err != nil {
		t.Fatalf("DoRequest: %v", err)
	}
	if gotPath != "/api/external/v1/model" {
		t.Errorf("server saw path %q, want %q", gotPath, "/api/external/v1/model")
	}
}
