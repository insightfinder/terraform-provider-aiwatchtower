// Copyright (c) InsightFinder Inc.
// SPDX-License-Identifier: Apache-2.0

package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"time"
)

const (
	// DefaultTimeout bounds a single HTTP attempt. Model writes fan out to RabbitMQ and Cassandra
	// on the backend, so they are slower than a plain CRUD service, but any single attempt that
	// has not answered within this window is treated as lost rather than waited on indefinitely.
	DefaultTimeout = 60 * time.Second

	// DefaultMaxRetries is the number of *additional* attempts made after the first one, so a
	// request is sent at most DefaultMaxRetries+1 times.
	DefaultMaxRetries = 3

	// retryBaseDelay is the first backoff interval; it doubles per retry (1s, 2s, 4s) and carries
	// jitter so that several resources retrying in the same apply do not synchronise.
	retryBaseDelay = time.Second

	// maxResponseBytes caps how much of a response body is read into memory. The largest expected
	// body is the model list, a few hundred kilobytes; anything beyond this is a malfunction or a
	// captive-portal style HTML page, and reading it in full helps nobody.
	maxResponseBytes = 16 << 20 // 16 MiB
)

// Client is the AI WatchTower API client.
type Client struct {
	BaseURL    string
	Username   string
	APIKey     string
	HTTPClient *http.Client

	// MaxRetries is the number of additional attempts after the first. Zero disables retrying.
	MaxRetries int
	// Timeout bounds each individual attempt.
	Timeout time.Duration
	// RetryBaseDelay is the first backoff interval, doubling per retry. Zero means the
	// package default; tests set it low to keep the suite fast.
	RetryBaseDelay time.Duration
}

// NewClient creates a new AI WatchTower API client.
func NewClient(baseURL, username, apiKey string) (*Client, error) {
	// Paths are joined by plain concatenation, so a configured base URL ending in "/" would
	// produce "//api/external/v1/..." — tolerated by most servers but not all, and a needless
	// way to make a working configuration fail.
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")

	if baseURL == "" {
		return nil, fmt.Errorf("base URL cannot be empty")
	}
	if username == "" {
		return nil, fmt.Errorf("username cannot be empty")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("API key cannot be empty")
	}

	return &Client{
		BaseURL:  baseURL,
		Username: username,
		APIKey:   apiKey,
		// No Timeout on the http.Client itself: each attempt gets its own deadline through the
		// request context, which also makes the attempt cancellable when Terraform is interrupted.
		HTTPClient:     &http.Client{},
		MaxRetries:     DefaultMaxRetries,
		Timeout:        DefaultTimeout,
		RetryBaseDelay: retryBaseDelay,
	}, nil
}

// retryableStatus reports whether an HTTP status is worth another attempt.
//
// 408 and 429 are explicit "try again" signals and 5xx indicates a server-side fault, which for
// this backend is usually a Cassandra or RabbitMQ hiccup that clears in seconds. Every other 4xx
// is a deterministic rejection — a bad key, a missing parameter, a conflicting model — and
// retrying it only multiplies the delay before the user sees the real error.
func retryableStatus(statusCode int) bool {
	return statusCode == http.StatusRequestTimeout ||
		statusCode == http.StatusTooManyRequests ||
		statusCode >= 500
}

// idempotent reports whether re-sending a method is safe.
//
// GET, PUT and DELETE can be repeated without changing the outcome: the model API's update and
// delete both converge on the same end state. POST creates, and the backend answers a second
// create with 409, so a POST that actually succeeded but whose response was lost would come back
// as a spurious "model already exists". That is true of a dropped connection just as much as of
// a 500 — neither tells us whether the server acted — so POST is not retried at all, and a
// genuinely transient failure surfaces to the operator instead of being papered over with a
// misleading conflict.
func idempotent(method string) bool {
	switch method {
	case http.MethodGet, http.MethodPut, http.MethodDelete:
		return true
	default:
		return false
	}
}

// backoff returns the delay before attempt n (1-based retry count), with +/-20% jitter.
func (c *Client) backoff(retry int) time.Duration {
	base := c.RetryBaseDelay
	if base <= 0 {
		base = retryBaseDelay
	}
	d := base << (retry - 1)
	jitter := time.Duration(rand.Int63n(int64(d/5)*2+1)) - d/5
	return d + jitter
}

// DoRequest performs an HTTP request against an /api/external/v1 endpoint, retrying transient
// failures.
//
// AI WatchTower's ApiKeyFilter is registered on "/api/external/v1/*" and authenticates on the
// X-Api-Key + X-User-Name header pair. Endpoints under /api/v1 are guarded by TokenFilter
// instead, which requires a browser session token, so they are unreachable from this client no
// matter what headers are supplied.
func (c *Client) DoRequest(ctx context.Context, method, path string, body interface{}) ([]byte, int, error) {
	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to marshal request body: %w", err)
		}
	}

	maxRetries := c.MaxRetries
	if maxRetries < 0 {
		maxRetries = 0
	}

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, 0, fmt.Errorf("request cancelled before retry %d: %w", attempt, ctx.Err())
			case <-time.After(c.backoff(attempt)):
			}
		}

		respBody, statusCode, err := c.doOnce(ctx, method, path, payload)

		if err != nil {
			lastErr = err
			// A transport failure may mean the request never arrived, or that the response was
			// lost after the server acted on it. Retrying is safe for idempotent methods only.
			if idempotent(method) && attempt < maxRetries && ctx.Err() == nil {
				continue
			}
			return nil, 0, err
		}

		if retryableStatus(statusCode) && idempotent(method) && attempt < maxRetries {
			lastErr = fmt.Errorf("HTTP %d", statusCode)
			continue
		}

		return respBody, statusCode, nil
	}

	// Only reachable when the final attempt was itself a retryable failure.
	return nil, 0, fmt.Errorf("request failed after %d attempts: %w", maxRetries+1, lastErr)
}

// doOnce performs a single attempt, bounded by the client's per-attempt timeout.
func (c *Client) doOnce(ctx context.Context, method, path string, payload []byte) ([]byte, int, error) {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	attemptCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var reqBody io.Reader
	if payload != nil {
		reqBody = bytes.NewReader(payload)
	}

	url := c.BaseURL + path
	req, err := http.NewRequestWithContext(attemptCtx, method, url, reqBody)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("X-User-Name", c.Username)
	req.Header.Set("X-Api-Key", c.APIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		// Never wrap the raw error alone: it embeds the full URL, and for this API the URL can
		// carry the user name. The path is enough to locate the failure.
		return nil, 0, fmt.Errorf("request to %s %s failed: %w", method, path, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("failed to read response body: %w", err)
	}

	return respBody, resp.StatusCode, nil
}
