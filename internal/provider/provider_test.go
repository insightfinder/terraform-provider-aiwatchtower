// Copyright (c) InsightFinder Inc.
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"

	"github.com/insightfinder/terraform-provider-aiwatchtower/internal/provider/client"
)

func TestProviderMetadata(t *testing.T) {
	p := New("test")()

	resp := &provider.MetadataResponse{}
	p.Metadata(context.Background(), provider.MetadataRequest{}, resp)

	if resp.TypeName != "aiwatchtower" {
		t.Errorf("expected type name %q, got %q", "aiwatchtower", resp.TypeName)
	}
	if resp.Version != "test" {
		t.Errorf("expected version %q, got %q", "test", resp.Version)
	}
}

func TestProviderSchema(t *testing.T) {
	p := New("test")()

	resp := &provider.SchemaResponse{}
	p.Schema(context.Background(), provider.SchemaRequest{}, resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
	}

	for _, name := range []string{"base_url", "username", "api_key"} {
		attr, ok := resp.Schema.Attributes[name]
		if !ok {
			t.Errorf("provider schema is missing attribute %q", name)
			continue
		}
		if !attr.IsOptional() {
			t.Errorf("attribute %q should be optional so it can come from an environment variable", name)
		}
	}

	for _, name := range []string{"username", "api_key"} {
		if !resp.Schema.Attributes[name].IsSensitive() {
			t.Errorf("attribute %q must be marked sensitive", name)
		}
	}
}

func TestProviderResources(t *testing.T) {
	p := New("test")()

	expected := map[string]bool{
		"aiwatchtower_model": false,
	}

	resources := p.Resources(context.Background())
	if len(resources) != len(expected) {
		t.Errorf("expected %d resources, got %d", len(expected), len(resources))
	}

	for _, fn := range resources {
		r := fn()
		resp := &resource.MetadataResponse{}
		r.Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "aiwatchtower"}, resp)

		seen, known := expected[resp.TypeName]
		if !known {
			t.Errorf("unexpected resource %q", resp.TypeName)
			continue
		}
		if seen {
			t.Errorf("resource %q registered more than once", resp.TypeName)
		}
		expected[resp.TypeName] = true
	}

	for name, seen := range expected {
		if !seen {
			t.Errorf("resource %q was not registered", name)
		}
	}
}

func TestModelID(t *testing.T) {
	got := modelID("user@company.com", "Fraud-Guard", "V1.0.0")
	want := "user@company.com@Fraud-Guard@V1.0.0"
	if got != want {
		t.Errorf("modelID() = %q, want %q", got, want)
	}
}

func TestParseModelImportID(t *testing.T) {
	tests := []struct {
		name                         string
		id                           string
		wantUser, wantModel, wantVer string
		wantErr                      bool
	}{
		{
			name:      "email user name",
			id:        "user@company.com@Fraud-Guard@V1.0.0",
			wantUser:  "user@company.com",
			wantModel: "Fraud-Guard",
			wantVer:   "V1.0.0",
		},
		{
			name:      "plain user name",
			id:        "service_account@tf-smoke@V0.0.1",
			wantUser:  "service_account",
			wantModel: "tf-smoke",
			wantVer:   "V0.0.1",
		},
		{
			name:      "model name containing a dash and dots in the version",
			id:        "u@a-b-c@1.2.3",
			wantUser:  "u",
			wantModel: "a-b-c",
			wantVer:   "1.2.3",
		},
		{name: "no separators", id: "justamodel", wantErr: true},
		{name: "only one separator", id: "user@model", wantErr: true},
		{name: "empty version", id: "user@model@", wantErr: true},
		{name: "empty model name", id: "user@@V1", wantErr: true},
		{name: "leading separator", id: "@model@V1", wantErr: true},
		{name: "empty id", id: "", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			user, model, ver, err := parseModelImportID(tc.id)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseModelImportID(%q) = (%q, %q, %q, nil), want an error", tc.id, user, model, ver)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseModelImportID(%q): %v", tc.id, err)
			}
			if user != tc.wantUser || model != tc.wantModel || ver != tc.wantVer {
				t.Errorf("parseModelImportID(%q) = (%q, %q, %q), want (%q, %q, %q)",
					tc.id, user, model, ver, tc.wantUser, tc.wantModel, tc.wantVer)
			}
		})
	}
}

func TestParseModelImportIDRoundTripsModelID(t *testing.T) {
	user, model, ver := "user@company.com", "Fraud-Guard", "V1.0.0"
	id := modelID(user, model, ver)

	gotUser, gotModel, gotVer, err := parseModelImportID(id)
	if err != nil {
		t.Fatalf("parseModelImportID(%q): %v", id, err)
	}
	if gotUser != user || gotModel != model || gotVer != ver {
		t.Errorf("round trip of %q gave (%q, %q, %q)", id, gotUser, gotModel, gotVer)
	}
}

func TestStringOrNull(t *testing.T) {
	if v := stringOrNull(""); !v.IsNull() {
		t.Error("an empty backend value must become null so that Read reports it as drift")
	}
	if v := stringOrNull("x"); v.ValueString() != "x" {
		t.Errorf("stringOrNull(\"x\") = %q", v.ValueString())
	}
}

func TestMarkNewDataSources(t *testing.T) {
	config := &client.ModelConfig{
		DataSources: []client.ModelDataSource{
			{Name: "InputLog"},
			{Name: "OutputLog"},
		},
	}
	markNewDataSources(config, map[string]bool{"InputLog": true})

	if config.DataSources[0].IsNew {
		t.Error("an existing data source was flagged as new; its project would be re-provisioned")
	}
	if !config.DataSources[1].IsNew {
		t.Error("an added data source was not flagged as new; the backend would not provision it")
	}
}

func TestMarkNewDataSourcesWithNoPriorState(t *testing.T) {
	config := &client.ModelConfig{DataSources: []client.ModelDataSource{{Name: "A"}, {Name: "B"}}}
	markNewDataSources(config, map[string]bool{})

	for i, ds := range config.DataSources {
		if !ds.IsNew {
			t.Errorf("data source %d was not flagged as new although state knows of none", i)
		}
	}
}

func TestModelDataSourcesAreUnordered(t *testing.T) {
	r := NewModelResource()
	resp := &resource.SchemaResponse{}
	r.Schema(context.Background(), resource.SchemaRequest{}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
	}

	for _, name := range []string{"data_sources", "monitor_info"} {
		attr, ok := resp.Schema.Attributes[name]
		if !ok {
			t.Errorf("schema is missing attribute %q", name)
			continue
		}
		// The backend keeps both of these in a Java Set and hands them back in hash order, so a
		// list attribute reports the reshuffle as a diff that apply can never settle.
		if _, isSet := attr.(schema.SetNestedAttribute); !isSet {
			t.Errorf("attribute %q is %T; it must be a SetNestedAttribute because the backend "+
				"returns it unordered", name, attr)
		}
	}
}
