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
	"testing"
)

const monitorJSON = `{
  "userName":"u","monitorName":"dq","monitorNameDisplayName":"dq","monitorType":"DATA_QUALITY",
  "modelName":"Fraud-Guard","modelNameDisplayName":"Fraud-Guard","version":"V1.0.0","versionDisplayName":"V1.0.0",
  "environment":"Production",
  "dataSources":[{"name":"InputLog","dataType":"Log","dataSource":"Custom","isNew":false}],
  "monitorDesc":"d",
  "monitorScope":[{"type":"field","keyword":"amount","include":true}],
  "monitorSetting":{"type":"DATA_QUALITY","groupNameKey":"merchant","volumeDetectionMode":"AI"}
}`

func TestGetMonitorReadsTheUIShape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, monitorJSON)
	}))
	defer srv.Close()

	got, err := newTestClient(t, srv).GetMonitor(context.Background(), "u", "dq")
	if err != nil {
		t.Fatalf("GetMonitor: %v", err)
	}
	if got == nil {
		t.Fatal("GetMonitor returned nil for a monitor that is present")
	}

	// The read shape names the version "version"; the write shape names it "modelVersion".
	if got.ModelVersion != "V1.0.0" {
		t.Errorf("ModelVersion = %q, want %q", got.ModelVersion, "V1.0.0")
	}
	if got.ModelName != "Fraud-Guard" {
		t.Errorf("ModelName = %q, want %q", got.ModelName, "Fraud-Guard")
	}
	if got.Description != "d" {
		t.Errorf("Description = %q, want %q", got.Description, "d")
	}
	if len(got.MonitorScope) != 1 || got.MonitorScope[0].Keyword != "amount" || !got.MonitorScope[0].Include {
		t.Errorf("MonitorScope = %+v, want one included 'amount' entry", got.MonitorScope)
	}
	if len(got.DataSources) != 1 || got.DataSources[0].Name != "InputLog" {
		t.Errorf("DataSources = %+v, want one entry named InputLog", got.DataSources)
	}

	setting, err := ParseDataQualitySetting(got.Setting)
	if err != nil {
		t.Fatalf("ParseDataQualitySetting: %v", err)
	}
	if setting.GroupNameKey == nil || *setting.GroupNameKey != "merchant" {
		t.Errorf("GroupNameKey = %v, want merchant", setting.GroupNameKey)
	}
}

// A monitor that is gone and a monitor the caller may not see both answer 403. Listing the user's
// monitors is what tells them apart.
func TestGetMonitorTreatsForbiddenAsAbsentWhenTheListIsReadable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("monitorName") != "" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, `{"message":"No permission to access the monitor"}`)
			return
		}
		_, _ = io.WriteString(w, `{"content":[],"totalElements":0}`)
	}))
	defer srv.Close()

	got, err := newTestClient(t, srv).GetMonitor(context.Background(), "u", "gone")
	if err != nil {
		t.Fatalf("GetMonitor: %v", err)
	}
	if got != nil {
		t.Errorf("GetMonitor = %+v, want nil for a monitor that is gone", got)
	}
}

func TestGetMonitorReportsForbiddenWhenTheListIsAlsoForbidden(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"message":"No permission to access the monitor"}`)
	}))
	defer srv.Close()

	got, err := newTestClient(t, srv).GetMonitor(context.Background(), "u", "dq")
	if err == nil {
		t.Fatalf("GetMonitor = %+v, want an error when the caller may not read monitors at all", got)
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("error = %q, want it to report the 403", err.Error())
	}
}

func TestCreateMonitorReportsTheDuplicateTypeRejection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusFailedDependency)
		_, _ = io.WriteString(w, `{"message":"Monitor type already existed"}`)
	}))
	defer srv.Close()

	err := newTestClient(t, srv).CreateMonitor(context.Background(), &MonitorConfig{MonitorName: "dq"})
	if err == nil {
		t.Fatal("CreateMonitor succeeded on a 424")
	}
	if !strings.Contains(err.Error(), "Monitor type already existed") {
		t.Errorf("error = %q, want the backend's message", err.Error())
	}
}

// The backend's update applies only the fields that arrive non-null, so clearing a description or
// a scope depends on them being sent as empty rather than omitted.
func TestUpdateMonitorSendsClearedFieldsExplicitly(t *testing.T) {
	var body map[string]json.RawMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("request body is not JSON: %v", err)
		}
		_, _ = io.WriteString(w, `{"message":"ok"}`)
	}))
	defer srv.Close()

	cfg := &MonitorConfig{UserName: "u", MonitorName: "dq", Description: ""}
	if err := newTestClient(t, srv).UpdateMonitor(context.Background(), cfg); err != nil {
		t.Fatalf("UpdateMonitor: %v", err)
	}

	desc, ok := body["monitorDesc"]
	if !ok {
		t.Fatal("monitorDesc was omitted; the backend would keep the previous description")
	}
	if string(desc) != `""` {
		t.Errorf("monitorDesc = %s, want an empty string", desc)
	}

	scope, ok := body["monitorScope"]
	if !ok {
		t.Fatal("monitorScope was omitted; the backend would keep the previous scope")
	}
	if string(scope) != `[]` {
		t.Errorf("monitorScope = %s, want an empty array", scope)
	}
}

func TestDeleteMonitorIsQuietWhenTheMonitorIsAlreadyGone(t *testing.T) {
	deleted := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			deleted = true
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if r.URL.Query().Get("monitorName") != "" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = io.WriteString(w, `{"content":[]}`)
	}))
	defer srv.Close()

	if err := newTestClient(t, srv).DeleteMonitor(context.Background(), "u", "gone"); err != nil {
		t.Fatalf("DeleteMonitor: %v", err)
	}
	if deleted {
		t.Error("DeleteMonitor issued a DELETE for a monitor that was already gone")
	}
}

func TestDroppedDataSourcesNamesWhatTheBackendDiscarded(t *testing.T) {
	sent := []ModelDataSource{{Name: "InputLog"}, {Name: "Latency"}, {Name: "Trace"}}
	stored := []ModelDataSource{{Name: "inputlog"}, {Name: "Trace"}}

	dropped := DroppedDataSources(sent, stored)
	if len(dropped) != 1 || dropped[0] != "Latency" {
		t.Errorf("DroppedDataSources = %v, want [Latency]", dropped)
	}
}

func TestDroppedDataSourcesIsEmptyWhenEverythingWasStored(t *testing.T) {
	sources := []ModelDataSource{{Name: "InputLog"}, {Name: "Trace"}}
	if dropped := DroppedDataSources(sources, sources); len(dropped) != 0 {
		t.Errorf("DroppedDataSources = %v, want none", dropped)
	}
}

// The setting travels through the backend's Gson layer, which names JSON by the Java field name.
func TestDataQualitySettingUsesTheBackendFieldNames(t *testing.T) {
	primary := true
	order := int64(3)
	fieldType := "NUMBER"
	group := "merchant"

	setting := NewDataQualitySetting()
	setting.GroupNameKey = &group
	setting.Keys = map[string]DataQualityFieldSetting{
		"amount": {
			FieldType:        &fieldType,
			DataQualityTypes: []string{"MISSING_DATA"},
			Order:            &order,
			IsPrimaryKey:     &primary,
		},
	}

	raw, err := setting.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	for _, name := range []string{`"type":"DATA_QUALITY"`, `"groupNameKey"`, `"fieldType"`, `"dataQualityTypes"`, `"isPrimaryKey"`} {
		if !strings.Contains(string(raw), name) {
			t.Errorf("serialized setting is missing %s: %s", name, raw)
		}
	}
	if strings.Contains(string(raw), `"primaryKey"`) {
		t.Errorf(`serialized setting uses "primaryKey"; the backend reads "isPrimaryKey": %s`, raw)
	}
}

func TestDataQualitySettingOmitsUnsetFields(t *testing.T) {
	raw, err := NewDataQualitySetting().Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(raw) != `{"type":"DATA_QUALITY"}` {
		t.Errorf("Marshal = %s, want only the discriminator", raw)
	}
}

func TestUpdateMonitorAcceptsCreatedStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"message":"Monitor updated successfully."}`)
	}))
	defer srv.Close()

	cfg := &MonitorConfig{UserName: "u", MonitorName: "dq"}
	if err := newTestClient(t, srv).UpdateMonitor(context.Background(), cfg); err != nil {
		t.Fatalf("UpdateMonitor rejected the backend's success status: %v", err)
	}
}
