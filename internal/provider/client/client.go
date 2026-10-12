// Copyright (c) InsightFinder Inc.
// SPDX-License-Identifier: Apache-2.0

package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	DefaultTimeout = 60 * time.Second

	DefaultMaxRetries = 3

	retryBaseDelay = time.Second

	maxResponseBytes = 16 << 20
)

type Client struct {
	BaseURL    string
	Username   string
	APIKey     string
	HTTPClient *http.Client

	MaxRetries     int
	Timeout        time.Duration
	RetryBaseDelay time.Duration
}

func NewClient(baseURL, username, apiKey string) (*Client, error) {
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
		BaseURL:        baseURL,
		Username:       username,
		APIKey:         apiKey,
		HTTPClient:     &http.Client{},
		MaxRetries:     DefaultMaxRetries,
		Timeout:        DefaultTimeout,
		RetryBaseDelay: retryBaseDelay,
	}, nil
}

func retryableStatus(statusCode int) bool {
	return statusCode == http.StatusRequestTimeout ||
		statusCode == http.StatusTooManyRequests ||
		statusCode >= 500
}

func idempotent(method string) bool {
	switch method {
	case http.MethodGet, http.MethodPut, http.MethodDelete:
		return true
	default:
		return false
	}
}

func (c *Client) backoff(retry int) time.Duration {
	base := c.RetryBaseDelay
	if base <= 0 {
		base = retryBaseDelay
	}
	d := base << (retry - 1)
	jitter := time.Duration(rand.Int63n(int64(d/5)*2+1)) - d/5
	return d + jitter
}

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

	return nil, 0, fmt.Errorf("request failed after %d attempts: %w", maxRetries+1, lastErr)
}

func pathWithoutQuery(path string) string {
	if i := strings.IndexByte(path, '?'); i >= 0 {
		return path[:i]
	}
	return path
}

func transportCause(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) && ue.Err != nil {
		return ue.Err
	}
	return err
}

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
		return nil, 0, fmt.Errorf("request to %s %s failed: %w", method, pathWithoutQuery(path), transportCause(err))
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("failed to read response body: %w", err)
	}

	return respBody, resp.StatusCode, nil
}
