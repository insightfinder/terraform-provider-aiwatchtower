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

// ModelDataSource mirrors the backend's ModelDataSource entity.
//
// Name is a *suffix*, not a full project name: the backend derives the UIE project name as
// sanitize(modelName + "-" + modelVersion + "-" + name). Passing a full project name here
// produces a doubled prefix, which is why the resource documents this field as a suffix and
// never exposes the backend's existInUIE flag.
type ModelDataSource struct {
	Name       string `json:"name"`
	DataType   string `json:"dataType"`
	DataSource string `json:"dataSource,omitempty"`
	// IsNew is a request flag, not state: ModelSetupService clears it once the project has been
	// provisioned, so what comes back is always false regardless of what was sent. (It reads back
	// at all only because the GET endpoint serializes with Gson, which ignores the @JsonProperty
	// WRITE_ONLY that governs the Jackson-parsed request side.) The provider sets it itself rather
	// than exposing it as a schema attribute — it is the flag ModelDTO.hasNewDataSource() checks to
	// decide whether to enqueue project creation at all, so a user-settable value would only ever
	// produce drift.
	IsNew bool `json:"isNew"`
}

// ModelUBLSetting mirrors the backend's ModelUBLSetting entity.
type ModelUBLSetting struct {
	Dimension int64 `json:"dimension"`
}

// MonitorInfo is read-only metadata the backend attaches to a model.
type MonitorInfo struct {
	MonitorName string `json:"monitorName,omitempty"`
	MonitorType string `json:"monitorType,omitempty"`
}

// ModelDetail mirrors the backend's ModelDetail (the model's "metadata" blob).
//
// Only the fields the provider manages are declared. Round-tripping an unknown field is not
// possible anyway: the backend rebuilds this object from its own entity on every read.
type ModelDetail struct {
	Environment string            `json:"environment,omitempty"`
	Description string            `json:"description,omitempty"`
	DataSources []ModelDataSource `json:"dataSources,omitempty"`
	UBLSetting  *ModelUBLSetting  `json:"ublSetting,omitempty"`
}

// ModelConfig is the provider-side representation of a model.
type ModelConfig struct {
	UserName     string
	ModelName    string
	ModelVersion string
	ModelType    string
	Environment  string
	Description  string
	DataSources  []ModelDataSource
	UBLDimension *int64

	// Read-only, populated by GetModel.
	ModelID      string
	AnomalyCount int64
	MonitorInfo  []MonitorInfo
}

// modelDTO mirrors the backend's ModelDTO on the wire.
type modelDTO struct {
	UserName       string        `json:"userName,omitempty"`
	ModelName      string        `json:"modelName,omitempty"`
	ModelType      string        `json:"modelType,omitempty"`
	ModelID        string        `json:"modelId,omitempty"`
	ModelVersion   string        `json:"modelVersion,omitempty"`
	Environment    string        `json:"environment,omitempty"`
	Description    string        `json:"description,omitempty"`
	AnomalyCount   int64         `json:"anomalyCount,omitempty"`
	ModelMetadata  *ModelDetail  `json:"modelMetadata,omitempty"`
	MonitorInfoSet []MonitorInfo `json:"monitorInfoSet,omitempty"`
	// OldModelVersion drives the backend's rename branch (ModelDTO.isUpdateModelVersion()). It is
	// deliberately not a schema attribute: the create endpoint rejects any payload carrying it,
	// and on update the provider fills it from prior state so a version change is an in-place
	// rename rather than a destroy/recreate.
	OldModelVersion string `json:"oldModelVersion,omitempty"`
}

// ifResponse mirrors the backend's IFResponse envelope used by the write endpoints.
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

// errorMessage pulls the backend's human-readable message out of a response body, falling back
// to the raw body when it is not an IFResponse envelope.
func errorMessage(body []byte) string {
	var r ifResponse
	if err := json.Unmarshal(body, &r); err == nil && r.Message != "" {
		return r.Message
	}
	return strings.TrimSpace(string(body))
}

// GetModel fetches a single model by its (userName, modelName, modelVersion) identity.
// Returns (nil, nil) when the model does not exist.
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

	// This endpoint returns a bare JSON array of ModelDTO, not an IFResponse envelope.
	var models []modelDTO
	if err := json.Unmarshal(body, &models); err != nil {
		return nil, fmt.Errorf("failed to parse model response: %w", err)
	}

	// modelName/modelVersion are optional filters on the backend, so narrow the result here
	// rather than trusting that exactly one entry came back.
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

// CreateModel creates a model.
//
// Project provisioning is asynchronous: the backend only enqueues a ModelSetupPayload on
// RabbitMQ, and ModelSetupConsumer creates the UIE projects and mapping rows afterwards. A 200
// here means the model record exists, not that its data sources are ready.
func (c *Client) CreateModel(ctx context.Context, config *ModelConfig) error {
	dto := config.toDTO("")
	// Every data source is new on create; this is what makes the backend enqueue project setup.
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

// UpdateModel updates an existing model. oldVersion must be the model version currently recorded
// in state; when it differs from config.ModelVersion the backend performs an in-place version
// rename (moving the model record, its mapping relation and its monitors) instead of rejecting
// the change.
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

// DeleteModel removes a model.
//
// The endpoint takes a list of keys and ModelService.deleteModels reports success only when
// every one of them was deleted, so asking it to delete a model that is already gone fails the
// whole call with HTTP 500 — a routine situation for Terraform, which must be able to destroy a
// resource somebody already removed by hand. The existence check therefore comes first: it turns
// the common "already gone" case into a no-op instead of an error that the retry layer would
// also spend several seconds re-attempting. The check is repeated after a failed delete to cover
// the case where the model disappeared between the two calls.
func (c *Client) DeleteModel(ctx context.Context, userName, modelName, modelVersion string) error {
	existing, err := c.GetModel(ctx, userName, modelName, modelVersion)
	if err == nil && existing == nil {
		return nil
	}

	payload := []map[string]string{{
		"userName":  userName,
		"modelName": modelName,
		// ModelKey's field is "version", not "modelVersion"; the wrong name deserializes to null
		// and the delete silently matches nothing.
		"version": modelVersion,
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
