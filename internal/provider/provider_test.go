// Copyright (c) InsightFinder Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/resource"
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

	// Credentials must never be echoed into plan output or logs.
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
	// The import ID format has to survive a user name that is an email address, which carries an
	// "@" of its own.
	got := modelID("user@company.com", "Fraud-Guard", "V1.0.0")
	want := "user@company.com@Fraud-Guard@V1.0.0"
	if got != want {
		t.Errorf("modelID() = %q, want %q", got, want)
	}
}
