// Copyright (c) InsightFinder Inc.
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/insightfinder/terraform-provider-aiwatchtower/internal/provider/client"
)

func TestParseMonitorImportIDSplitsOnTheLastSeparator(t *testing.T) {
	userName, monitorName, err := parseMonitorImportID("user@company.com@Fraud-Guard-DQ")
	if err != nil {
		t.Fatalf("parseMonitorImportID: %v", err)
	}
	if userName != "user@company.com" {
		t.Errorf("userName = %q, want %q", userName, "user@company.com")
	}
	if monitorName != "Fraud-Guard-DQ" {
		t.Errorf("monitorName = %q, want %q", monitorName, "Fraud-Guard-DQ")
	}
}

func TestParseMonitorImportIDRejectsMalformedIDs(t *testing.T) {
	for _, id := range []string{"", "nouser", "@monitor", "user@"} {
		if _, _, err := parseMonitorImportID(id); err == nil {
			t.Errorf("parseMonitorImportID(%q) succeeded, want an error", id)
		}
	}
}

func dataQualityFixture() *dataQualityModel {
	return &dataQualityModel{
		GroupNameKey: types.StringValue("merchant"),
		Keys: map[string]fieldSettingModel{
			"amount": {
				FieldType:        types.StringValue("NUMBER"),
				DataQualityTypes: []types.String{types.StringValue("MISSING_DATA"), types.StringValue("ANOMALOUS_DATA_VALUE")},
				RegexConfig: map[string]regexSettingModel{
					"CUSTOM": {
						ConditionRegex:   []types.String{types.StringValue("^a")},
						DataFillingRegex: []types.String{types.StringValue("b$")},
					},
				},
				CustomRegex:     types.StringValue("^[0-9]+$"),
				CustomName:      types.StringValue("digits"),
				Threshold:       types.StringValue("5"),
				ConsistencyKeys: []consistencyKeyModel{{Key: types.StringValue("currency"), Value: types.StringValue("USD")}},
				Order:           types.Int64Value(1),
				IsPrimaryKey:    types.BoolValue(true),
			},
		},
		GroupKeys: map[string]groupFieldsModel{
			"Walmart": {Fields: map[string]fieldSettingModel{
				"sku": {
					FieldType:        types.StringValue("STRING"),
					DataQualityTypes: []types.String{types.StringValue("MISSING_DATA")},
					Order:            types.Int64Value(2),
					IsPrimaryKey:     types.BoolNull(),
				},
			}},
		},
		VolumeDetectionEnabled:       types.BoolValue(true),
		VolumeDetectionMode:          types.StringValue("MANUAL"),
		VolumeThresholds:             &volumeThresholdsModel{Warning: types.Float64Value(15), Critical: types.Float64Value(30)},
		VolumeSensitivity:            types.StringValue("HIGH"),
		VolumeWarningBaselineFactor:  types.Float64Value(1.08),
		VolumeCriticalBaselineFactor: types.Float64Value(1.15),
		VolumeReferenceFileName:      types.StringValue("volumes.csv"),
		VolumeReference: &volumeReferenceModel{
			Holidays: []volumeHolidayModel{{
				Name:  types.StringValue("Christmas"),
				Start: types.StringValue("12-24"),
				End:   types.StringValue("12-26"),
			}},
			Volumes: []volumeEntryModel{
				{Volume: types.Float64Value(1000000)},
				{DayOfWeek: types.StringValue("SATURDAY"), Volume: types.Float64Value(600000)},
				{Group: types.StringValue("Walmart"), Date: types.StringValue("2026-11-27"), Volume: types.Float64Value(3000000)},
			},
		},
		SchemaCheckEnabled: types.BoolValue(true),
		SchemaCheck:        types.ObjectNull(schemaCheckObjectType().AttrTypes),
	}
}

func TestDataQualityRoundTripsThroughTheClientShape(t *testing.T) {
	ctx := context.Background()

	in := dataQualityFixture()
	sc, diags := types.ObjectValueFrom(ctx, schemaCheckObjectType().AttrTypes, schemaCheckModel{
		FieldOrder:       types.BoolValue(true),
		UnexpectedFields: types.BoolValue(false),
		LostFields:       types.BoolValue(true),
	})
	if diags.HasError() {
		t.Fatalf("building schema_check: %v", diags)
	}
	in.SchemaCheck = sc

	setting, diags := dataQualityToClient(ctx, in)
	if diags.HasError() {
		t.Fatalf("dataQualityToClient: %v", diags)
	}

	raw, err := setting.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	parsed, err := client.ParseDataQualitySetting(raw)
	if err != nil {
		t.Fatalf("ParseDataQualitySetting: %v", err)
	}

	out, diags := dataQualityFromClient(ctx, parsed)
	if diags.HasError() {
		t.Fatalf("dataQualityFromClient: %v", diags)
	}

	if out.GroupNameKey != in.GroupNameKey {
		t.Errorf("GroupNameKey = %v, want %v", out.GroupNameKey, in.GroupNameKey)
	}

	amount, ok := out.Keys["amount"]
	if !ok {
		t.Fatalf("Keys = %+v, want an 'amount' entry", out.Keys)
	}
	if len(amount.DataQualityTypes) != 2 {
		t.Errorf("DataQualityTypes = %v, want two entries", amount.DataQualityTypes)
	}
	if amount.Threshold != types.StringValue("5") {
		t.Errorf("Threshold = %v, want \"5\"", amount.Threshold)
	}
	if amount.IsPrimaryKey != types.BoolValue(true) {
		t.Errorf("IsPrimaryKey = %v, want true", amount.IsPrimaryKey)
	}
	if len(amount.ConsistencyKeys) != 1 || amount.ConsistencyKeys[0].Value != types.StringValue("USD") {
		t.Errorf("ConsistencyKeys = %+v, want one USD entry", amount.ConsistencyKeys)
	}
	rc, ok := amount.RegexConfig["CUSTOM"]
	if !ok || len(rc.ConditionRegex) != 1 || rc.ConditionRegex[0] != types.StringValue("^a") {
		t.Errorf("RegexConfig = %+v, want the CUSTOM condition regex", amount.RegexConfig)
	}

	sku, ok := out.GroupKeys["Walmart"].Fields["sku"]
	if !ok {
		t.Fatalf("GroupKeys = %+v, want Walmart/sku", out.GroupKeys)
	}
	if sku.Order != types.Int64Value(2) {
		t.Errorf("GroupKeys Walmart/sku order = %v, want 2", sku.Order)
	}
	// Absent in the fixture, and absent from the JSON, so it has to come back null rather than false.
	if !sku.IsPrimaryKey.IsNull() {
		t.Errorf("GroupKeys Walmart/sku is_primary_key = %v, want null", sku.IsPrimaryKey)
	}

	if out.VolumeThresholds == nil || out.VolumeThresholds.Warning.ValueFloat64() != 15 {
		t.Errorf("VolumeThresholds = %+v, want warning 15", out.VolumeThresholds)
	}
	if out.VolumeReference == nil || len(out.VolumeReference.Volumes) != 3 {
		t.Fatalf("VolumeReference = %+v, want three volume entries", out.VolumeReference)
	}
	if out.VolumeReference.Holidays[0].Start != types.StringValue("12-24") {
		t.Errorf("holiday start = %v, want 12-24", out.VolumeReference.Holidays[0].Start)
	}
	// The default entry carries no selector, so all three selectors have to stay null.
	def := out.VolumeReference.Volumes[0]
	if !def.Date.IsNull() || !def.DayOfWeek.IsNull() || !def.Holiday.IsNull() || !def.Group.IsNull() {
		t.Errorf("default volume entry = %+v, want every selector null", def)
	}

	var gotCheck schemaCheckModel
	if d := out.SchemaCheck.As(ctx, &gotCheck, basetypes.ObjectAsOptions{}); d.HasError() {
		t.Fatalf("reading schema_check back: %v", d)
	}
	if gotCheck.FieldOrder != types.BoolValue(true) || gotCheck.UnexpectedFields != types.BoolValue(false) || gotCheck.LostFields != types.BoolValue(true) {
		t.Errorf("schema_check = %+v, want true/false/true", gotCheck)
	}
}

// Everything the plan already settled has to survive a write untouched; only the attributes the
// backend decides may be filled in from the read-back.
func TestFillComputedDataQualityOnlyWritesUnknowns(t *testing.T) {
	ctx := context.Background()

	mode := "AI"
	sensitivity := "LOW"
	warning := 2.0
	critical := 3.0
	yes := true

	actual := &client.DataQualitySetting{
		VolumeDetectionMode:          &mode,
		VolumeSensitivity:            &sensitivity,
		VolumeWarningBaselineFactor:  &warning,
		VolumeCriticalBaselineFactor: &critical,
		SchemaCheck:                  &client.SchemaCheck{FieldOrder: &yes, UnexpectedFields: &yes, LostFields: &yes},
	}

	planned := &dataQualityModel{
		VolumeDetectionMode:          types.StringValue("MANUAL"),
		VolumeSensitivity:            types.StringUnknown(),
		VolumeWarningBaselineFactor:  types.Float64Value(1.5),
		VolumeCriticalBaselineFactor: types.Float64Unknown(),
		SchemaCheck:                  types.ObjectUnknown(schemaCheckObjectType().AttrTypes),
	}

	if diags := fillComputedDataQuality(ctx, planned, actual); diags.HasError() {
		t.Fatalf("fillComputedDataQuality: %v", diags)
	}

	if planned.VolumeDetectionMode != types.StringValue("MANUAL") {
		t.Errorf("VolumeDetectionMode = %v, want the planned MANUAL to survive", planned.VolumeDetectionMode)
	}
	if planned.VolumeWarningBaselineFactor.ValueFloat64() != 1.5 {
		t.Errorf("VolumeWarningBaselineFactor = %v, want the planned 1.5 to survive", planned.VolumeWarningBaselineFactor)
	}
	if planned.VolumeSensitivity != types.StringValue("LOW") {
		t.Errorf("VolumeSensitivity = %v, want it filled from the backend", planned.VolumeSensitivity)
	}
	if planned.VolumeCriticalBaselineFactor.ValueFloat64() != 3 {
		t.Errorf("VolumeCriticalBaselineFactor = %v, want it filled from the backend", planned.VolumeCriticalBaselineFactor)
	}
	if planned.SchemaCheck.IsUnknown() {
		t.Error("schema_check is still unknown after the read-back")
	}
}

func TestFillComputedDataQualityFillsOnlyTheUnknownSubChecks(t *testing.T) {
	ctx := context.Background()

	yes, no := true, false
	actual := &client.DataQualitySetting{
		SchemaCheck: &client.SchemaCheck{FieldOrder: &yes, UnexpectedFields: &yes, LostFields: &yes},
	}

	partial, diags := types.ObjectValueFrom(ctx, schemaCheckObjectType().AttrTypes, schemaCheckModel{
		FieldOrder:       types.BoolValue(no),
		UnexpectedFields: types.BoolUnknown(),
		LostFields:       types.BoolUnknown(),
	})
	if diags.HasError() {
		t.Fatalf("building schema_check: %v", diags)
	}

	planned := &dataQualityModel{
		VolumeDetectionMode:          types.StringNull(),
		VolumeSensitivity:            types.StringNull(),
		VolumeWarningBaselineFactor:  types.Float64Null(),
		VolumeCriticalBaselineFactor: types.Float64Null(),
		SchemaCheck:                  partial,
	}

	if diags := fillComputedDataQuality(ctx, planned, actual); diags.HasError() {
		t.Fatalf("fillComputedDataQuality: %v", diags)
	}

	var got schemaCheckModel
	if d := planned.SchemaCheck.As(ctx, &got, basetypes.ObjectAsOptions{}); d.HasError() {
		t.Fatalf("reading schema_check back: %v", d)
	}
	if got.FieldOrder != types.BoolValue(false) {
		t.Errorf("field_order = %v, want the planned false to survive", got.FieldOrder)
	}
	if got.UnexpectedFields != types.BoolValue(true) || got.LostFields != types.BoolValue(true) {
		t.Errorf("schema_check = %+v, want the unknown sub-checks filled from the backend", got)
	}
}

func TestDataQualityToClientOmitsUnknownComputedAttributes(t *testing.T) {
	ctx := context.Background()

	planned := &dataQualityModel{
		GroupNameKey:                 types.StringValue("merchant"),
		VolumeDetectionMode:          types.StringUnknown(),
		VolumeSensitivity:            types.StringUnknown(),
		VolumeWarningBaselineFactor:  types.Float64Unknown(),
		VolumeCriticalBaselineFactor: types.Float64Unknown(),
		SchemaCheck:                  types.ObjectUnknown(schemaCheckObjectType().AttrTypes),
	}

	setting, diags := dataQualityToClient(ctx, planned)
	if diags.HasError() {
		t.Fatalf("dataQualityToClient: %v", diags)
	}

	if setting.VolumeDetectionMode != nil || setting.VolumeSensitivity != nil ||
		setting.VolumeWarningBaselineFactor != nil || setting.VolumeCriticalBaselineFactor != nil ||
		setting.SchemaCheck != nil {
		t.Errorf("setting = %+v, want every unknown attribute left out so the backend defaults it", setting)
	}

	raw, err := setting.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(raw) != `{"type":"DATA_QUALITY","groupNameKey":"merchant"}` {
		t.Errorf("Marshal = %s, want only the known attributes", raw)
	}
}

// Exercising the real protocol-level schema conversion is what catches a schema the framework
// rejects at startup, which no amount of unit testing the conversion helpers would find.
func TestMonitorSchemaSurvivesProtocolConversion(t *testing.T) {
	ctx := context.Background()

	server, err := providerserver.NewProtocol6WithError(New("test")())()
	if err != nil {
		t.Fatalf("NewProtocol6WithError: %v", err)
	}

	resp, err := server.GetProviderSchema(ctx, &tfprotov6.GetProviderSchemaRequest{})
	if err != nil {
		t.Fatalf("GetProviderSchema: %v", err)
	}
	for _, d := range resp.Diagnostics {
		if d.Severity == tfprotov6.DiagnosticSeverityError {
			t.Errorf("schema diagnostic: %s - %s", d.Summary, d.Detail)
		}
	}

	monitor, ok := resp.ResourceSchemas["aiwatchtower_monitor"]
	if !ok {
		t.Fatalf("aiwatchtower_monitor is missing from the provider schema")
	}

	seen := map[string]bool{}
	for _, attr := range monitor.Block.Attributes {
		seen[attr.Name] = true
	}
	for _, name := range []string{"data_quality", "monitor_setting_json", "data_sources", "monitor_scope", "monitor_type"} {
		if !seen[name] {
			t.Errorf("attribute %q is missing from the monitor schema", name)
		}
	}
}

func monitorConfigWith(ctx context.Context, t *testing.T, overrides map[string]tftypes.Value) tfsdk.Config {
	t.Helper()

	schemaResp := &resource.SchemaResponse{}
	NewMonitorResource().Schema(ctx, resource.SchemaRequest{}, schemaResp)
	if schemaResp.Diagnostics.HasError() {
		t.Fatalf("building the monitor schema: %v", schemaResp.Diagnostics)
	}

	objType, ok := schemaResp.Schema.Type().TerraformType(ctx).(tftypes.Object)
	if !ok {
		t.Fatalf("monitor schema is not an object type")
	}

	raw := make(map[string]tftypes.Value, len(objType.AttributeTypes))
	for name, typ := range objType.AttributeTypes {
		if v, overridden := overrides[name]; overridden {
			raw[name] = v
			continue
		}
		raw[name] = tftypes.NewValue(typ, nil)
	}

	return tfsdk.Config{Schema: schemaResp.Schema, Raw: tftypes.NewValue(objType, raw)}
}

func monitorAttributeType(ctx context.Context, t *testing.T, name string) tftypes.Type {
	t.Helper()

	schemaResp := &resource.SchemaResponse{}
	NewMonitorResource().Schema(ctx, resource.SchemaRequest{}, schemaResp)
	objType := schemaResp.Schema.Type().TerraformType(ctx).(tftypes.Object)

	typ, ok := objType.AttributeTypes[name]
	if !ok {
		t.Fatalf("attribute %q is missing from the monitor schema", name)
	}
	return typ
}

// A configuration may hold an unknown anywhere — data_quality assigned from a variable, or from
// another resource's output. Reading the whole configuration into the resource model would fail on
// the pointer and slice fields long before any rule was checked.
func TestValidateConfigToleratesAnUnknownDataQuality(t *testing.T) {
	ctx := context.Background()

	config := monitorConfigWith(ctx, t, map[string]tftypes.Value{
		"monitor_type":  tftypes.NewValue(tftypes.String, "DATA_QUALITY"),
		"monitor_name":  tftypes.NewValue(tftypes.String, "dq"),
		"model_name":    tftypes.NewValue(tftypes.String, "m"),
		"model_version": tftypes.NewValue(tftypes.String, "v"),
		"data_quality":  tftypes.NewValue(monitorAttributeType(ctx, t, "data_quality"), tftypes.UnknownValue),
	})

	resp := &resource.ValidateConfigResponse{}
	(&monitorResource{}).ValidateConfig(ctx, resource.ValidateConfigRequest{Config: config}, resp)

	for _, d := range resp.Diagnostics.Errors() {
		if strings.Contains(strings.ToLower(d.Detail()), "unknown value") {
			t.Fatalf("validation choked on an unknown: %s - %s", d.Summary(), d.Detail())
		}
	}

	// data_sources was left null, so validation has to have run all the way to the last rule.
	if len(resp.Diagnostics.Errors()) != 1 || resp.Diagnostics.Errors()[0].Summary() != "Missing Data Sources" {
		t.Errorf("diagnostics = %v, want only the missing data sources error", resp.Diagnostics.Errors())
	}
}

func TestValidateConfigRejectsBothSettingForms(t *testing.T) {
	ctx := context.Background()

	config := monitorConfigWith(ctx, t, map[string]tftypes.Value{
		"monitor_type":         tftypes.NewValue(tftypes.String, "DATA_QUALITY"),
		"monitor_name":         tftypes.NewValue(tftypes.String, "dq"),
		"data_quality":         tftypes.NewValue(monitorAttributeType(ctx, t, "data_quality"), tftypes.UnknownValue),
		"monitor_setting_json": tftypes.NewValue(tftypes.String, `{"type":"DATA_QUALITY"}`),
	})

	resp := &resource.ValidateConfigResponse{}
	(&monitorResource{}).ValidateConfig(ctx, resource.ValidateConfigRequest{Config: config}, resp)

	if len(resp.Diagnostics.Errors()) != 1 || resp.Diagnostics.Errors()[0].Summary() != "Conflicting Monitor Setting" {
		t.Errorf("diagnostics = %v, want the conflicting setting error", resp.Diagnostics.Errors())
	}
}

func TestValidateConfigRejectsDataQualityOnAnotherMonitorType(t *testing.T) {
	ctx := context.Background()

	config := monitorConfigWith(ctx, t, map[string]tftypes.Value{
		"monitor_type":  tftypes.NewValue(tftypes.String, "MODEL_DRIFT"),
		"monitor_name":  tftypes.NewValue(tftypes.String, "drift"),
		"model_name":    tftypes.NewValue(tftypes.String, "m"),
		"model_version": tftypes.NewValue(tftypes.String, "v"),
		"data_quality":  tftypes.NewValue(monitorAttributeType(ctx, t, "data_quality"), tftypes.UnknownValue),
	})

	resp := &resource.ValidateConfigResponse{}
	(&monitorResource{}).ValidateConfig(ctx, resource.ValidateConfigRequest{Config: config}, resp)

	var found bool
	for _, d := range resp.Diagnostics.Errors() {
		if d.Summary() == "Wrong Monitor Setting Form" {
			found = true
		}
	}
	if !found {
		t.Errorf("diagnostics = %v, want data_quality refused on a MODEL_DRIFT monitor", resp.Diagnostics.Errors())
	}
}
