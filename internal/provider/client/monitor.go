// Copyright (c) InsightFinder Inc.
// SPDX-License-Identifier: Apache-2.0

package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

const MonitorTypeDataQuality = "DATA_QUALITY"

type MonitorScope struct {
	Type    string `json:"type"`
	Keyword string `json:"keyword"`
	Include bool   `json:"include"`
}

type MonitorConfig struct {
	UserName     string
	MonitorName  string
	MonitorType  string
	ModelName    string
	ModelVersion string
	Environment  string
	Description  string
	DataSources  []ModelDataSource
	MonitorScope []MonitorScope
	Setting      json.RawMessage
}

type monitorDTO struct {
	UserName     string            `json:"userName,omitempty"`
	MonitorName  string            `json:"monitorName,omitempty"`
	MonitorType  string            `json:"monitorType,omitempty"`
	ModelName    string            `json:"modelName,omitempty"`
	ModelVersion string            `json:"modelVersion,omitempty"`
	Environment  string            `json:"environment,omitempty"`
	DataSources  []ModelDataSource `json:"dataSources,omitempty"`
	MonitorDesc  string            `json:"monitorDesc,omitempty"`
	MonitorScope []MonitorScope    `json:"monitorScope,omitempty"`
	Setting      json.RawMessage   `json:"monitorSetting,omitempty"`
}

// monitorDesc and monitorScope are sent even when empty: the backend's update applies only the
// fields that arrive non-null, so omitting them would keep the previous description and scope
// instead of clearing them.
type monitorUpdateDTO struct {
	UserName     string          `json:"userName,omitempty"`
	MonitorName  string          `json:"monitorName,omitempty"`
	MonitorDesc  string          `json:"monitorDesc"`
	MonitorScope []MonitorScope  `json:"monitorScope"`
	Setting      json.RawMessage `json:"monitorSetting,omitempty"`
}

type monitorUIDTO struct {
	UserName     string            `json:"userName"`
	MonitorName  string            `json:"monitorName"`
	MonitorType  string            `json:"monitorType"`
	ModelName    string            `json:"modelName"`
	Version      string            `json:"version"`
	Environment  string            `json:"environment"`
	DataSources  []ModelDataSource `json:"dataSources"`
	MonitorDesc  string            `json:"monitorDesc"`
	MonitorScope []MonitorScope    `json:"monitorScope"`
	Setting      json.RawMessage   `json:"monitorSetting"`
}

func (c *MonitorConfig) toDTO() *monitorDTO {
	return &monitorDTO{
		UserName:     c.UserName,
		MonitorName:  c.MonitorName,
		MonitorType:  c.MonitorType,
		ModelName:    c.ModelName,
		ModelVersion: c.ModelVersion,
		Environment:  c.Environment,
		DataSources:  c.DataSources,
		MonitorDesc:  c.Description,
		MonitorScope: c.MonitorScope,
		Setting:      c.Setting,
	}
}

func (c *MonitorConfig) toUpdateDTO() *monitorUpdateDTO {
	scope := c.MonitorScope
	if scope == nil {
		scope = []MonitorScope{}
	}
	return &monitorUpdateDTO{
		UserName:     c.UserName,
		MonitorName:  c.MonitorName,
		MonitorDesc:  c.Description,
		MonitorScope: scope,
		Setting:      c.Setting,
	}
}

func monitorFromUIDTO(userName string, dto *monitorUIDTO) *MonitorConfig {
	return &MonitorConfig{
		UserName:     userName,
		MonitorName:  dto.MonitorName,
		MonitorType:  dto.MonitorType,
		ModelName:    dto.ModelName,
		ModelVersion: dto.Version,
		Environment:  dto.Environment,
		Description:  dto.MonitorDesc,
		DataSources:  dto.DataSources,
		MonitorScope: dto.MonitorScope,
		Setting:      dto.Setting,
	}
}

// GetMonitor returns nil without an error when the monitor does not exist.
//
// The backend answers both "no such monitor" and "you may not see it" with HTTP 403, so a 403 is
// disambiguated by listing the user's monitors: a successful list proves the caller is allowed to
// read them, which makes the 403 on the single monitor mean it is gone.
func (c *Client) GetMonitor(ctx context.Context, userName, monitorName string) (*MonitorConfig, error) {
	params := url.Values{}
	params.Set("userName", userName)
	params.Set("monitorName", monitorName)

	path := fmt.Sprintf("/api/external/v1/monitor?%s", params.Encode())
	body, statusCode, err := c.DoRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	switch statusCode {
	case 200:
		var dto monitorUIDTO
		if err := json.Unmarshal(body, &dto); err != nil {
			return nil, fmt.Errorf("failed to parse monitor response: %w", err)
		}
		if dto.MonitorName == "" {
			return nil, nil
		}
		return monitorFromUIDTO(userName, &dto), nil
	case 204, 404:
		return nil, nil
	case 401, 403:
		readable, listErr := c.canListMonitors(ctx, userName)
		if listErr != nil {
			return nil, listErr
		}
		if readable {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get monitor: HTTP %d - %s", statusCode, errorMessage(body))
	default:
		return nil, fmt.Errorf("failed to get monitor: HTTP %d - %s", statusCode, errorMessage(body))
	}
}

func (c *Client) canListMonitors(ctx context.Context, userName string) (bool, error) {
	params := url.Values{}
	params.Set("userName", userName)
	params.Set("pageNumber", "0")
	params.Set("pageSize", "1")

	path := fmt.Sprintf("/api/external/v1/monitor?%s", params.Encode())
	body, statusCode, err := c.DoRequest(ctx, "GET", path, nil)
	if err != nil {
		return false, err
	}
	if statusCode == 200 {
		return true, nil
	}
	if statusCode == 401 || statusCode == 403 {
		return false, nil
	}
	return false, fmt.Errorf("failed to list monitors: HTTP %d - %s", statusCode, errorMessage(body))
}

func (c *Client) CreateMonitor(ctx context.Context, config *MonitorConfig) error {
	body, statusCode, err := c.DoRequest(ctx, "POST", "/api/external/v1/monitor", config.toDTO())
	if err != nil {
		return err
	}

	switch statusCode {
	case 200, 201:
		return nil
	case 409:
		return fmt.Errorf("monitor %q already exists: %s", config.MonitorName, errorMessage(body))
	case 424:
		return fmt.Errorf("monitor %q cannot be created: %s", config.MonitorName, errorMessage(body))
	default:
		return fmt.Errorf("failed to create monitor: HTTP %d - %s", statusCode, errorMessage(body))
	}
}

func (c *Client) UpdateMonitor(ctx context.Context, config *MonitorConfig) error {
	body, statusCode, err := c.DoRequest(ctx, "PUT", "/api/external/v1/monitor", config.toUpdateDTO())
	if err != nil {
		return err
	}

	if statusCode == 204 {
		return fmt.Errorf("failed to update monitor %q: the backend reports it does not exist", config.MonitorName)
	}
	if statusCode != 200 && statusCode != 201 {
		return fmt.Errorf("failed to update monitor: HTTP %d - %s", statusCode, errorMessage(body))
	}
	return nil
}

// DeleteMonitor tolerates a monitor that is already gone. The backend's delete reports success
// without checking that anything was removed, so a failure is re-checked with a read before it is
// reported.
func (c *Client) DeleteMonitor(ctx context.Context, userName, monitorName string) error {
	existing, err := c.GetMonitor(ctx, userName, monitorName)
	if err == nil && existing == nil {
		return nil
	}

	payload := []map[string]string{{
		"userName":    userName,
		"monitorName": monitorName,
	}}

	body, statusCode, err := c.DoRequest(ctx, "DELETE", "/api/external/v1/monitor", payload)
	if err != nil {
		return err
	}
	if statusCode == 200 {
		return nil
	}

	if after, readErr := c.GetMonitor(ctx, userName, monitorName); readErr == nil && after == nil {
		return nil
	}

	return fmt.Errorf("failed to delete monitor: HTTP %d - %s", statusCode, errorMessage(body))
}

// DroppedDataSources returns the names of the data sources that were sent but not stored. The
// backend silently drops data sources whose type the monitor type cannot use, so the only way to
// learn about it is to compare the write against the read-back.
func DroppedDataSources(sent, stored []ModelDataSource) []string {
	kept := make(map[string]bool, len(stored))
	for _, ds := range stored {
		kept[strings.ToLower(ds.Name)] = true
	}

	var dropped []string
	for _, ds := range sent {
		if !kept[strings.ToLower(ds.Name)] {
			dropped = append(dropped, ds.Name)
		}
	}
	return dropped
}
