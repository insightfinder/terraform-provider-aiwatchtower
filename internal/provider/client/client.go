// Copyright (c) InsightFinder Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client is the AI WatchTower API client.
type Client struct {
	BaseURL    string
	Username   string
	APIKey     string
	HTTPClient *http.Client
}

// NewClient creates a new AI WatchTower API client.
func NewClient(baseURL, username, apiKey string) (*Client, error) {
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
		HTTPClient: &http.Client{
			Timeout: time.Second * 60,
		},
	}, nil
}

// DoRequest performs an HTTP request against an /api/external/v1 endpoint.
//
// AI WatchTower's ApiKeyFilter is registered on "/api/external/v1/*" and authenticates on the
// X-Api-Key + X-User-Name header pair. Endpoints under /api/v1 are guarded by TokenFilter
// instead, which requires a browser session token, so they are unreachable from this client no
// matter what headers are supplied.
func (c *Client) DoRequest(method, path string, body interface{}) ([]byte, int, error) {
	var reqBody io.Reader
	if body != nil {
		jsonBody, err := json.Marshal(body)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqBody = bytes.NewBuffer(jsonBody)
	}

	url := fmt.Sprintf("%s%s", c.BaseURL, path)
	req, err := http.NewRequest(method, url, reqBody)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("X-User-Name", c.Username)
	req.Header.Set("X-Api-Key", c.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("failed to read response body: %w", err)
	}

	return respBody, resp.StatusCode, nil
}
