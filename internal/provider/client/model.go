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

type ModelDataSource struct {
	Name       string `json:"name"`
	DataType   string `json:"dataType"`
	DataSource string `json:"dataSource,omitempty"`
	IsNew      bool   `json:"isNew"`
}

type ModelUBLSetting struct {
	Dimension int64 `json:"dimension"`
}

type MonitorInfo struct {
	MonitorName string `json:"monitorName,omitempty"`
	MonitorType string `json:"monitorType,omitempty"`
}

type ModelDetail struct {
	Environment string            `json:"environment,omitempty"`
	Description string            `json:"description,omitempty"`
	DataSources []ModelDataSource `json:"dataSources,omitempty"`
	UBLSetting  *ModelUBLSetting  `json:"ublSetting,omitempty"`
}

type ModelConfig struct {
	UserName     string
	ModelName    string
	ModelVersion string
	ModelType    string
	Environment  string
	Description  string
	DataSources  []ModelDataSource
	UBLDimension *int64

	ModelID      string
	AnomalyCount int64
	MonitorInfo  []MonitorInfo
}

type modelDTO struct {
	UserName        string        `json:"userName,omitempty"`
	ModelName       string        `json:"modelName,omitempty"`
	ModelType       string        `json:"modelType,omitempty"`
	ModelID         string        `json:"modelId,omitempty"`
	ModelVersion    string        `json:"modelVersion,omitempty"`
	Environment     string        `json:"environment,omitempty"`
	Description     string        `json:"description,omitempty"`
	AnomalyCount    int64         `json:"anomalyCount,omitempty"`
	ModelMetadata   *ModelDetail  `json:"modelMetadata,omitempty"`
	MonitorInfoSet  []MonitorInfo `json:"monitorInfoSet,omitempty"`
	OldModelVersion string        `json:"oldModelVersion,omitempty"`
}

type ifResponse struct {
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (c *ModelConfig) toDTO(oldVersion string) *modelDTO {
	dto := &modelDTO{
		UserName:        c.UserName,
		ModelName:       c.ModelName,
		ModelType:       c.ModelType,
		ModelVersion:    c.ModelVersion,
		Environment:     c.Environment,
		Description:     c.Description,
		OldModelVersion: oldVersion,
		ModelMetadata: &ModelDetail{
			Environment: c.Environment,
			Description: c.Description,
			DataSources: c.DataSources,
		},
	}
	if c.UBLDimension != nil {
		dto.ModelMetadata.UBLSetting = &ModelUBLSetting{Dimension: *c.UBLDimension}
	}
	return dto
}

func errorMessage(body []byte) string {
	var r ifResponse
	if err := json.Unmarshal(body, &r); err == nil && r.Message != "" {
		return r.Message
	}
	return strings.TrimSpace(string(body))
}

func (c *Client) GetModel(ctx context.Context, userName, modelName, modelVersion string) (*ModelConfig, error) {
	params := url.Values{}
	params.Set("userName", userName)
	params.Set("modelName", modelName)
	params.Set("modelVersion", modelVersion)

	path := fmt.Sprintf("/api/external/v1/model?%s", params.Encode())
	body, statusCode, err := c.DoRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	if statusCode == 404 || statusCode == 204 {
		return nil, nil
	}
	if statusCode != 200 {
		return nil, fmt.Errorf("failed to get model: HTTP %d - %s", statusCode, errorMessage(body))
	}

	var models []modelDTO
	if err := json.Unmarshal(body, &models); err != nil {
		return nil, fmt.Errorf("failed to parse model response: %w", err)
	}

	for _, m := range models {
		if !strings.EqualFold(m.ModelName, modelName) || !strings.EqualFold(m.ModelVersion, modelVersion) {
			continue
		}
		cfg := &ModelConfig{
			UserName:     userName,
			ModelName:    m.ModelName,
			ModelVersion: m.ModelVersion,
			ModelType:    m.ModelType,
			ModelID:      m.ModelID,
			Environment:  m.Environment,
			Description:  m.Description,
			AnomalyCount: m.AnomalyCount,
			MonitorInfo:  m.MonitorInfoSet,
		}
		if m.ModelMetadata != nil {
			if cfg.Environment == "" {
				cfg.Environment = m.ModelMetadata.Environment
			}
			if cfg.Description == "" {
				cfg.Description = m.ModelMetadata.Description
			}
			cfg.DataSources = m.ModelMetadata.DataSources
			if m.ModelMetadata.UBLSetting != nil {
				d := m.ModelMetadata.UBLSetting.Dimension
				cfg.UBLDimension = &d
			}
		}
		return cfg, nil
	}

	return nil, nil
}

func (c *Client) CreateModel(ctx context.Context, config *ModelConfig) error {
	dto := config.toDTO("")
	for i := range dto.ModelMetadata.DataSources {
		dto.ModelMetadata.DataSources[i].IsNew = true
	}

	body, statusCode, err := c.DoRequest(ctx, "POST", "/api/external/v1/model", dto)
	if err != nil {
		return err
	}

	if statusCode == 409 {
		return fmt.Errorf("model %s/%s already exists: %s", config.ModelName, config.ModelVersion, errorMessage(body))
	}
	if statusCode != 200 {
		return fmt.Errorf("failed to create model: HTTP %d - %s", statusCode, errorMessage(body))
	}
	return nil
}

func (c *Client) UpdateModel(ctx context.Context, config *ModelConfig, oldVersion string) error {
	dto := config.toDTO(oldVersion)

	body, statusCode, err := c.DoRequest(ctx, "PUT", "/api/external/v1/model", dto)
	if err != nil {
		return err
	}

	if statusCode != 200 {
		return fmt.Errorf("failed to update model: HTTP %d - %s", statusCode, errorMessage(body))
	}
	return nil
}

func (c *Client) DeleteModel(ctx context.Context, userName, modelName, modelVersion string) error {
	existing, err := c.GetModel(ctx, userName, modelName, modelVersion)
	if err == nil && existing == nil {
		return nil
	}

	payload := []map[string]string{{
		"userName":  userName,
		"modelName": modelName,
		"version":   modelVersion,
	}}

	body, statusCode, err := c.DoRequest(ctx, "DELETE", "/api/external/v1/model", payload)
	if err != nil {
		return err
	}
	if statusCode == 200 {
		return nil
	}

	if after, readErr := c.GetModel(ctx, userName, modelName, modelVersion); readErr == nil && after == nil {
		return nil
	}

	return fmt.Errorf("failed to delete model: HTTP %d - %s", statusCode, errorMessage(body))
}
