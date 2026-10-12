// Copyright (c) InsightFinder Inc.
// SPDX-License-Identifier: Apache-2.0

package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

const modelListJSON = `[
  {"userName":"u","modelName":"other","modelVersion":"V1","modelType":"OpenAI"},
  {"userName":"u","modelName":"Fraud-Guard","modelVersion":"V1.0.0","modelType":"OpenAI","modelId":"42",
   "anomalyCount":7,
   "modelMetadata":{"environment":"Production","description":"d",
     "dataSources":[{"name":"InputLog","dataType":"Log","dataSource":"Custom","isNew":false}],
     "ublSetting":{"dimension":128}},
   "monitorInfoSet":[{"monitorName":"m1","monitorType":"LLM_PERFORMANCE"}]}
]`

func TestGetModelSelectsTheMatchingEntry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, modelListJSON)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	got, err := c.GetModel(context.Background(), "u", "Fraud-Guard", "V1.0.0")
	if err != nil {
		t.Fatalf("GetModel: %v", err)
	}
	if got == nil {
		t.Fatal("GetModel returned nil for a model that is present")
	}

	if got.ModelID != "42" {
		t.Errorf("ModelID = %q, want %q", got.ModelID, "42")
	}
	if got.AnomalyCount != 7 {
		t.Errorf("AnomalyCount = %d, want 7", got.AnomalyCount)
	}
	if got.Environment != "Production" {
		t.Errorf("Environment = %q, want %q", got.Environment, "Production")
	}
	if got.Description != "d" {
		t.Errorf("Description = %q, want %q", got.Description, "d")
	}
	if got.UBLDimension == nil || *got.UBLDimension != 128 {
		t.Errorf("UBLDimension = %v, want 128", got.UBLDimension)
	}
	if len(got.DataSources) != 1 || got.DataSources[0].Name != "InputLog" {
		t.Errorf("DataSources = %+v, want one entry named InputLog", got.DataSources)
	}
	if len(got.MonitorInfo) != 1 || got.MonitorInfo[0].MonitorType != "LLM_PERFORMANCE" {
		t.Errorf("MonitorInfo = %+v, want one LLM_PERFORMANCE entry", got.MonitorInfo)
	}
}

func TestGetModelReturnsNilWhenAbsent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, modelListJSON)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	got, err := c.GetModel(context.Background(), "u", "Fraud-Guard", "V9.9.9")
	if err != nil {
		t.Fatalf("GetModel: %v", err)
	}
	if got != nil {
		t.Errorf("GetModel = %+v, want nil for a version that does not exist", got)
	}
}

func TestGetModelTreats204AsAbsent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	got, err := c.GetModel(context.Background(), "u", "m", "v")
	if err != nil || got != nil {
		t.Errorf("GetModel = (%+v, %v), want (nil, nil)", got, err)
	}
}

func TestCreateModelMarksDataSourcesNew(t *testing.T) {
	var body modelDTO
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	err := c.CreateModel(context.Background(), &ModelConfig{
		UserName: "u", ModelName: "m", ModelVersion: "v", ModelType: "OpenAI",
		DataSources: []ModelDataSource{{Name: "InputLog", DataType: "Log"}},
	})
	if err != nil {
		t.Fatalf("CreateModel: %v", err)
	}

	if body.ModelMetadata == nil || len(body.ModelMetadata.DataSources) != 1 {
		t.Fatalf("request carried no data sources: %+v", body.ModelMetadata)
	}
	if !body.ModelMetadata.DataSources[0].IsNew {
		t.Error("data source was sent with isNew=false; the backend would not provision its project")
	}
	if body.OldModelVersion != "" {
		t.Errorf("create sent oldModelVersion=%q, which the backend rejects", body.OldModelVersion)
	}
}

func TestCreateModelReportsConflict(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, `{"message":"Fail to create the model, model has been already created"}`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	err := c.CreateModel(context.Background(), &ModelConfig{UserName: "u", ModelName: "m", ModelVersion: "v"})
	if err == nil {
		t.Fatal("expected an error on 409")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error %q does not explain that the model already exists", err)
	}
}

func TestUpdateModelSendsOldVersion(t *testing.T) {
	var body modelDTO
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	err := c.UpdateModel(context.Background(), &ModelConfig{
		UserName: "u", ModelName: "m", ModelVersion: "V2",
	}, "V1")
	if err != nil {
		t.Fatalf("UpdateModel: %v", err)
	}

	if body.OldModelVersion != "V1" {
		t.Errorf("oldModelVersion = %q, want %q", body.OldModelVersion, "V1")
	}
	if body.ModelVersion != "V2" {
		t.Errorf("modelVersion = %q, want %q", body.ModelVersion, "V2")
	}
}

func TestDeleteModelSkipsTheCallWhenAlreadyGone(t *testing.T) {
	var deletes int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			atomic.AddInt32(&deletes, 1)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = io.WriteString(w, `[]`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	if err := c.DeleteModel(context.Background(), "u", "m", "v"); err != nil {
		t.Fatalf("DeleteModel on an absent model must succeed, got: %v", err)
	}
	if got := atomic.LoadInt32(&deletes); got != 0 {
		t.Errorf("DELETE was sent %d times for a model that does not exist, want 0", got)
	}
}

func TestDeleteModelSendsVersionKey(t *testing.T) {
	var payload []map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			_ = json.NewDecoder(r.Body).Decode(&payload)
			w.WriteHeader(http.StatusOK)
			return
		}
		_, _ = io.WriteString(w, modelListJSON)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	if err := c.DeleteModel(context.Background(), "u", "Fraud-Guard", "V1.0.0"); err != nil {
		t.Fatalf("DeleteModel: %v", err)
	}

	if len(payload) != 1 {
		t.Fatalf("payload = %+v, want a single key", payload)
	}
	if payload[0]["version"] != "V1.0.0" {
		t.Errorf("key = %+v, want version=V1.0.0", payload[0])
	}
	if _, wrong := payload[0]["modelVersion"]; wrong {
		t.Error("payload used modelVersion; ModelKey only understands version")
	}
}

func TestDeleteModelSucceedsIfModelVanishesDuringDelete(t *testing.T) {
	var gets int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if atomic.AddInt32(&gets, 1) == 1 {
			_, _ = io.WriteString(w, modelListJSON)
			return
		}
		_, _ = io.WriteString(w, `[]`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	if err := c.DeleteModel(context.Background(), "u", "Fraud-Guard", "V1.0.0"); err != nil {
		t.Errorf("DeleteModel: %v, want success once the model is confirmed gone", err)
	}
}

func TestDeleteModelReportsRealFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"message":"Fail to delete the model"}`)
			return
		}
		_, _ = io.WriteString(w, modelListJSON)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	err := c.DeleteModel(context.Background(), "u", "Fraud-Guard", "V1.0.0")
	if err == nil {
		t.Fatal("expected an error when the model is still there after a failed delete")
	}
	if !strings.Contains(err.Error(), "Fail to delete the model") {
		t.Errorf("error %q does not carry the backend's message", err)
	}
}

func TestErrorMessageFallsBackToRawBody(t *testing.T) {
	if got := errorMessage([]byte(`{"message":"boom"}`)); got != "boom" {
		t.Errorf("errorMessage = %q, want %q", got, "boom")
	}
	if got := errorMessage([]byte("  <html>502</html>  ")); got != "<html>502</html>" {
		t.Errorf("errorMessage = %q, want the trimmed raw body", got)
	}
}

func TestGetModelSendsIdentityAsQueryParameters(t *testing.T) {
	var got map[string][]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query()
		_, _ = io.WriteString(w, `[]`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	if _, err := c.GetModel(context.Background(), "u", "Fraud-Guard", "V1.0.0"); err != nil {
		t.Fatalf("GetModel: %v", err)
	}

	for key, want := range map[string]string{
		"userName":     "u",
		"modelName":    "Fraud-Guard",
		"modelVersion": "V1.0.0",
	} {
		if len(got[key]) != 1 || got[key][0] != want {
			t.Errorf("query parameter %s = %v, want [%q]", key, got[key], want)
		}
	}
}
