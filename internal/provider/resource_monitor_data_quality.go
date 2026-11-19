// Copyright (c) InsightFinder Inc.
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"

	"github.com/insightfinder/terraform-provider-aiwatchtower/internal/provider/client"
)

type regexSettingModel struct {
	ConditionRegex   []types.String `tfsdk:"condition_regex"`
	DataFillingRegex []types.String `tfsdk:"data_filling_regex"`
}

type consistencyKeyModel struct {
	Key   types.String `tfsdk:"key"`
	Value types.String `tfsdk:"value"`
}

type fieldSettingModel struct {
	FieldType        types.String                 `tfsdk:"field_type"`
	DataQualityTypes []types.String               `tfsdk:"data_quality_types"`
	RegexConfig      map[string]regexSettingModel `tfsdk:"regex_config"`
	CustomRegex      types.String                 `tfsdk:"custom_regex"`
	CustomName       types.String                 `tfsdk:"custom_name"`
	Threshold        types.String                 `tfsdk:"threshold"`
	ConsistencyKeys  []consistencyKeyModel        `tfsdk:"consistency_keys"`
	Order            types.Int64                  `tfsdk:"order"`
	IsPrimaryKey     types.Bool                   `tfsdk:"is_primary_key"`

	DataQualityDescriptions map[string]types.String `tfsdk:"data_quality_descriptions"`
}

type groupFieldsModel struct {
	Fields map[string]fieldSettingModel `tfsdk:"fields"`
}

type volumeThresholdsModel struct {
	Warning  types.Float64 `tfsdk:"warning"`
	Critical types.Float64 `tfsdk:"critical"`
}

type volumeHolidayModel struct {
	Name  types.String `tfsdk:"name"`
	Start types.String `tfsdk:"start"`
	End   types.String `tfsdk:"end"`
}

type volumeEntryModel struct {
	Group     types.String  `tfsdk:"group"`
	Date      types.String  `tfsdk:"date"`
	DayOfWeek types.String  `tfsdk:"day_of_week"`
	Holiday   types.String  `tfsdk:"holiday"`
	Volume    types.Float64 `tfsdk:"volume"`
}

type volumeReferenceModel struct {
	Holidays []volumeHolidayModel `tfsdk:"holidays"`
	Volumes  []volumeEntryModel   `tfsdk:"volumes"`
}

type schemaCheckModel struct {
	FieldOrder       types.Bool `tfsdk:"field_order"`
	UnexpectedFields types.Bool `tfsdk:"unexpected_fields"`
	LostFields       types.Bool `tfsdk:"lost_fields"`
}

type dataQualityModel struct {
	UploadJSON           types.String `tfsdk:"upload_json"`
	GroupNameKey         types.String `tfsdk:"group_name_key"`
	GroupExtractionRegex types.String `tfsdk:"group_extraction_regex"`
	// map[string]fieldSettingModel and map[string]groupFieldsModel. They are framework maps rather
	// than Go maps because a plan holds them unknown until upload_json is expanded.
	Keys      types.Map `tfsdk:"keys"`
	GroupKeys types.Map `tfsdk:"group_keys"`

	VolumeDetectionEnabled       types.Bool             `tfsdk:"volume_detection_enabled"`
	VolumeDetectionMode          types.String           `tfsdk:"volume_detection_mode"`
	VolumeThresholds             *volumeThresholdsModel `tfsdk:"volume_thresholds"`
	VolumeSensitivity            types.String           `tfsdk:"volume_sensitivity"`
	VolumeWarningBaselineFactor  types.Float64          `tfsdk:"volume_warning_baseline_factor"`
	VolumeCriticalBaselineFactor types.Float64          `tfsdk:"volume_critical_baseline_factor"`
	VolumeReferenceFileName      types.String           `tfsdk:"volume_reference_file_name"`
	VolumeReference              *volumeReferenceModel  `tfsdk:"volume_reference"`

	SchemaCheckEnabled types.Bool   `tfsdk:"schema_check_enabled"`
	SchemaCheck        types.Object `tfsdk:"schema_check"`
}

func fieldSettingNestedObject() schema.NestedAttributeObject {
	return schema.NestedAttributeObject{
		Attributes: map[string]schema.Attribute{
			"field_type": schema.StringAttribute{
				Description: "Type the field's values are read as: STRING, NUMBER or CATEGORY. Required for ANOMALOUS_DATA_VALUE, which the backend accepts on NUMBER fields only.",
				Optional:    true,
			},
			"data_quality_types": schema.SetAttribute{
				Description: "Checks to run on the field: CONSISTENCY, MISSING_DATA, DATA_OUTLIERS, FIELD_TYPE_MISMATCH, QUALIFIED_DATA_RANGE, CUSTOM, ANOMALOUS_DATA_VALUE. Unordered, like the set the backend stores.",
				Optional:    true,
				ElementType: types.StringType,
			},
			"regex_config": schema.MapNestedAttribute{
				Description: "Per-check regular expressions, keyed by the check name used in data_quality_types.",
				Optional:    true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"condition_regex": schema.ListAttribute{
							Description: "Expressions selecting the records the check applies to.",
							Optional:    true,
							ElementType: types.StringType,
						},
						"data_filling_regex": schema.ListAttribute{
							Description: "Expressions extracting the value to check out of the record.",
							Optional:    true,
							ElementType: types.StringType,
						},
					},
				},
			},
			"custom_regex": schema.StringAttribute{
				Description: "Expression backing the CUSTOM check.",
				Optional:    true,
			},
			"custom_name": schema.StringAttribute{
				Description: "Display name of the CUSTOM check.",
				Optional:    true,
			},
			"threshold": schema.StringAttribute{
				Description: "Threshold of the check, as the backend stores it: a string, not a number.",
				Optional:    true,
			},
			"consistency_keys": schema.ListNestedAttribute{
				Description: "Fields whose values must agree for the CONSISTENCY check. Ordered, like the list the backend stores.",
				Optional:    true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"key": schema.StringAttribute{
							Description: "Name of the field to compare against.",
							Required:    true,
						},
						"value": schema.StringAttribute{
							Description: "Value the field must hold. Leave unset to compare the fields against each other.",
							Optional:    true,
						},
					},
				},
			},
			"order": schema.Int64Attribute{
				Description: "Position of the field in the Workbench's field table. A single counter across the whole table, not restarted per group.",
				Optional:    true,
			},
			"is_primary_key": schema.BoolAttribute{
				Description: "Whether the field takes part in the primary key. Records that share the same values of every primary key field within a group and period are flagged as duplicates.",
				Optional:    true,
			},
			"data_quality_descriptions": schema.MapAttribute{
				Description: "Description of a check, keyed by the check name used in data_quality_types. Shown after the check in the Workbench's csv export.",
				Optional:    true,
				ElementType: types.StringType,
			},
		},
	}
}

func groupFieldsNestedObject() schema.NestedAttributeObject {
	return schema.NestedAttributeObject{
		Attributes: map[string]schema.Attribute{
			"fields": schema.MapNestedAttribute{
				Description:  "Rules for this group, keyed by the event's JSON field name.",
				Required:     true,
				NestedObject: fieldSettingNestedObject(),
			},
		},
	}
}

func fieldSettingObjectType() attr.Type {
	return fieldSettingNestedObject().Type()
}

func groupFieldsObjectType() attr.Type {
	return groupFieldsNestedObject().Type()
}

func dataQualitySchema() schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		Description: "Settings of a DATA_QUALITY monitor. Mutually exclusive with monitor_setting_json, and the only supported form for this monitor type.",
		Optional:    true,
		Attributes: map[string]schema.Attribute{
			"upload_json": schema.StringAttribute{
				Description: "The Workbench's Upload JSON file, unchanged: a sample log event with an optional top-level \"$dataCheckTypes\" object. " +
					"The provider expands it into keys and group_keys as the Workbench does, and the plan shows the result. Every field of the sample event is sent, " +
					"in file order, so the schema check knows the whole event; a field gets checks only from its $dataCheckTypes entry. " +
					"Entries the Workbench would ignore are reported as plan warnings. Mutually exclusive with keys and group_keys. Not stored by the backend.",
				Optional: true,
			},
			"group_name_key": schema.StringAttribute{
				Description: "JSON key of the log event whose value names the group, for example \"merchant\". Leave unset to run every rule over all events together.",
				Optional:    true,
			},
			"group_extraction_regex": schema.StringAttribute{
				Description: "The Workbench's Auto Grouping regex. With upload_json and group_name_key, each field whose name it matches goes to the group named by " +
					"capture group 1 (or the whole match). Also used by the csv export to strip the group prefix from field names. " +
					"Evaluated with Go's RE2 syntax, so lookbehind and backreferences are not available.",
				Optional: true,
			},
			"keys": schema.MapNestedAttribute{
				Description:  "Rules that apply to every event regardless of group, keyed by the event's JSON field name. Computed from upload_json when that is set.",
				Optional:     true,
				Computed:     true,
				NestedObject: fieldSettingNestedObject(),
			},
			"group_keys": schema.MapNestedAttribute{
				Description:  "Rules that apply only to the events of one group, keyed by the group value. The same field can carry different rules in different groups. Computed from upload_json when that is set.",
				Optional:     true,
				Computed:     true,
				NestedObject: groupFieldsNestedObject(),
			},
			"volume_detection_enabled": schema.BoolAttribute{
				Description: "Whether the daily record count is compared against an expected volume.",
				Optional:    true,
			},
			"volume_detection_mode": schema.StringAttribute{
				Description: "How the volume verdict is reached: AI compares the day against a learned baseline band, MANUAL against the percentages in volume_thresholds. Defaults to AI.",
				Optional:    true,
				Computed:    true,
			},
			"volume_thresholds": schema.SingleNestedAttribute{
				Description: "Percent deviation from the expected volume that MANUAL mode reports on. Ignored in AI mode.",
				Optional:    true,
				Attributes: map[string]schema.Attribute{
					"warning": schema.Float64Attribute{
						Description: "Warning threshold, in percent. Defaults to 20, and must be greater than 0 and no greater than critical.",
						Optional:    true,
					},
					"critical": schema.Float64Attribute{
						Description: "Critical threshold, in percent. Defaults to 40, and must not exceed 100.",
						Optional:    true,
					},
				},
			},
			"volume_sensitivity": schema.StringAttribute{
				Description: "Width of the baseline band AI mode judges against: HIGH, MEDIUM_HIGH, MEDIUM, MEDIUM_LOW or LOW, strictest first. Defaults to MEDIUM_HIGH.",
				Optional:    true,
				Computed:    true,
			},
			"volume_warning_baseline_factor": schema.Float64Attribute{
				Description: "How far beyond the baseline band AI mode reports a warning. Defaults to 1.08.",
				Optional:    true,
				Computed:    true,
			},
			"volume_critical_baseline_factor": schema.Float64Attribute{
				Description: "How far beyond the baseline band AI mode reports a critical anomaly. Defaults to 1.15.",
				Optional:    true,
				Computed:    true,
			},
			"volume_reference_file_name": schema.StringAttribute{
				Description: "Name shown for the reference volumes in the Workbench. Display only.",
				Optional:    true,
			},
			"volume_reference": schema.SingleNestedAttribute{
				Description: "Expected daily volumes. Leave unset to judge every day against the learned baseline alone. " +
					"A day resolves to the first match of: exact date, named holiday, any holiday, weekday, default — and at each of those an entry naming a group beats one that does not.",
				Optional: true,
				Attributes: map[string]schema.Attribute{
					"holidays": schema.ListNestedAttribute{
						Description: "Recurring date ranges that volume entries can name. Also pushed to the project's holiday calendar so the learned baseline honours them.",
						Optional:    true,
						NestedObject: schema.NestedAttributeObject{
							Attributes: map[string]schema.Attribute{
								"name": schema.StringAttribute{
									Description: "Name of the holiday, referenced by a volume entry. Must be unique, and cannot be \"*\".",
									Required:    true,
								},
								"start": schema.StringAttribute{
									Description: "First day of the range as MM-DD. A range whose start is after its end wraps around the new year.",
									Required:    true,
								},
								"end": schema.StringAttribute{
									Description: "Last day of the range as MM-DD.",
									Required:    true,
								},
							},
						},
					},
					"volumes": schema.ListNestedAttribute{
						Description: "Expected record counts. At least one entry is required, and an entry with no selector at all is the default.",
						Optional:    true,
						NestedObject: schema.NestedAttributeObject{
							Attributes: map[string]schema.Attribute{
								"group": schema.StringAttribute{
									Description: "Group value this entry applies to. Leave unset to apply it to every group.",
									Optional:    true,
								},
								"date": schema.StringAttribute{
									Description: "Single calendar date as YYYY-MM-DD. At most one of date, day_of_week and holiday may be set.",
									Optional:    true,
								},
								"day_of_week": schema.StringAttribute{
									Description: "Weekday as MONDAY through SUNDAY, upper case — the backend compares the string exactly and does not normalize it.",
									Optional:    true,
								},
								"holiday": schema.StringAttribute{
									Description: "Name of a holiday listed in holidays, or \"*\" for any of them.",
									Optional:    true,
								},
								"volume": schema.Float64Attribute{
									Description: "Expected number of records for the day. Must not be negative.",
									Optional:    true,
								},
							},
						},
					},
				},
			},
			"schema_check_enabled": schema.BoolAttribute{
				Description: "Whether each record is checked against the expected set and order of fields.",
				Optional:    true,
			},
			"schema_check": schema.SingleNestedAttribute{
				Description: "Which schema sub-checks run. The backend fills in every sub-check left unset as false.",
				Optional:    true,
				Computed:    true,
				Attributes: map[string]schema.Attribute{
					"field_order": schema.BoolAttribute{
						Description: "Report records whose fields arrive in a different order.",
						Optional:    true,
						Computed:    true,
					},
					"unexpected_fields": schema.BoolAttribute{
						Description: "Report records carrying a field that is not expected.",
						Optional:    true,
						Computed:    true,
					},
					"lost_fields": schema.BoolAttribute{
						Description: "Report records missing a field that is expected.",
						Optional:    true,
						Computed:    true,
					},
				},
			},
		},
	}
}

func schemaCheckObjectType() types.ObjectType {
	return types.ObjectType{AttrTypes: map[string]attr.Type{
		"field_order":       types.BoolType,
		"unexpected_fields": types.BoolType,
		"lost_fields":       types.BoolType,
	}}
}

func dataQualityToClient(ctx context.Context, m *dataQualityModel) (*client.DataQualitySetting, diag.Diagnostics) {
	var diags diag.Diagnostics

	setting := client.NewDataQualitySetting()
	setting.GroupNameKey = stringPtr(m.GroupNameKey)
	setting.DataQualityGroupExtractionRegex = stringPtr(m.GroupExtractionRegex)

	keys, d := fieldSettingsFromMap(ctx, m.Keys)
	diags.Append(d...)
	groupKeys, d := groupFieldsFromMap(ctx, m.GroupKeys)
	diags.Append(d...)
	if diags.HasError() {
		return nil, diags
	}

	setting.Keys = fieldSettingsToClient(keys)
	if len(groupKeys) > 0 {
		setting.GroupKeys = make(map[string]map[string]client.DataQualityFieldSetting, len(groupKeys))
		for group, fields := range groupKeys {
			setting.GroupKeys[group] = fieldSettingsToClient(fields.Fields)
		}
	}

	setting.VolumeDetectionEnabled = boolPtr(m.VolumeDetectionEnabled)
	setting.VolumeDetectionMode = stringPtr(m.VolumeDetectionMode)
	setting.VolumeSensitivity = stringPtr(m.VolumeSensitivity)
	setting.VolumeWarningBaselineFactor = float64Ptr(m.VolumeWarningBaselineFactor)
	setting.VolumeCriticalBaselineFactor = float64Ptr(m.VolumeCriticalBaselineFactor)
	setting.VolumeReferenceFileName = stringPtr(m.VolumeReferenceFileName)
	setting.SchemaCheckEnabled = boolPtr(m.SchemaCheckEnabled)

	if m.VolumeThresholds != nil {
		setting.VolumeThresholds = &client.VolumeThresholds{
			Warning:  float64Ptr(m.VolumeThresholds.Warning),
			Critical: float64Ptr(m.VolumeThresholds.Critical),
		}
	}

	if m.VolumeReference != nil {
		ref := &client.VolumeReference{}
		for _, h := range m.VolumeReference.Holidays {
			ref.Holidays = append(ref.Holidays, client.VolumeHolidayEntry{
				Name:  h.Name.ValueString(),
				Start: h.Start.ValueString(),
				End:   h.End.ValueString(),
			})
		}
		for _, v := range m.VolumeReference.Volumes {
			ref.Volumes = append(ref.Volumes, client.VolumeEntry{
				Group:     stringPtr(v.Group),
				Date:      stringPtr(v.Date),
				DayOfWeek: stringPtr(v.DayOfWeek),
				Holiday:   stringPtr(v.Holiday),
				Volume:    float64Ptr(v.Volume),
			})
		}
		setting.VolumeReference = ref
	}

	if !m.SchemaCheck.IsNull() && !m.SchemaCheck.IsUnknown() {
		var sc schemaCheckModel
		diags.Append(m.SchemaCheck.As(ctx, &sc, basetypes.ObjectAsOptions{})...)
		if diags.HasError() {
			return nil, diags
		}
		setting.SchemaCheck = &client.SchemaCheck{
			FieldOrder:       boolPtr(sc.FieldOrder),
			UnexpectedFields: boolPtr(sc.UnexpectedFields),
			LostFields:       boolPtr(sc.LostFields),
		}
	}

	return setting, diags
}

func fieldSettingsToClient(fields map[string]fieldSettingModel) map[string]client.DataQualityFieldSetting {
	if len(fields) == 0 {
		return nil
	}

	out := make(map[string]client.DataQualityFieldSetting, len(fields))
	for name, f := range fields {
		setting := client.DataQualityFieldSetting{
			FieldType:    stringPtr(f.FieldType),
			CustomRegex:  stringPtr(f.CustomRegex),
			CustomName:   stringPtr(f.CustomName),
			Threshold:    stringPtr(f.Threshold),
			Order:        int64Ptr(f.Order),
			IsPrimaryKey: boolPtr(f.IsPrimaryKey),

			DataQualityTypes: make([]string, 0, len(f.DataQualityTypes)),
		}
		for _, t := range f.DataQualityTypes {
			setting.DataQualityTypes = append(setting.DataQualityTypes, t.ValueString())
		}
		for check, text := range f.DataQualityDescriptions {
			if text.IsNull() || text.IsUnknown() {
				continue
			}
			if setting.DataQualityDescriptions == nil {
				setting.DataQualityDescriptions = make(map[string]string, len(f.DataQualityDescriptions))
			}
			setting.DataQualityDescriptions[check] = text.ValueString()
		}
		if len(f.RegexConfig) > 0 {
			setting.RegexConfig = make(map[string]client.DataQualityRegexSetting, len(f.RegexConfig))
			for check, rc := range f.RegexConfig {
				setting.RegexConfig[check] = client.DataQualityRegexSetting{
					ConditionRegex:   stringSlice(rc.ConditionRegex),
					DataFillingRegex: stringSlice(rc.DataFillingRegex),
				}
			}
		}
		for _, ck := range f.ConsistencyKeys {
			setting.ConsistencyKeys = append(setting.ConsistencyKeys, client.ConsistencyKey{
				Key:   ck.Key.ValueString(),
				Value: stringPtr(ck.Value),
			})
		}
		out[name] = setting
	}
	return out
}

func dataQualityFromClient(ctx context.Context, setting *client.DataQualitySetting) (*dataQualityModel, diag.Diagnostics) {
	var diags diag.Diagnostics

	if setting == nil {
		return nil, diags
	}

	keys, d := fieldSettingsToMap(ctx, fieldSettingsFromClient(setting.Keys))
	diags.Append(d...)
	if diags.HasError() {
		return nil, diags
	}

	m := &dataQualityModel{
		UploadJSON:                   types.StringNull(),
		GroupNameKey:                 stringFromPtr(setting.GroupNameKey),
		GroupExtractionRegex:         stringFromPtr(setting.DataQualityGroupExtractionRegex),
		Keys:                         keys,
		GroupKeys:                    types.MapNull(groupFieldsObjectType()),
		VolumeDetectionEnabled:       boolFromPtr(setting.VolumeDetectionEnabled),
		VolumeDetectionMode:          stringFromPtr(setting.VolumeDetectionMode),
		VolumeSensitivity:            stringFromPtr(setting.VolumeSensitivity),
		VolumeWarningBaselineFactor:  float64FromPtr(setting.VolumeWarningBaselineFactor),
		VolumeCriticalBaselineFactor: float64FromPtr(setting.VolumeCriticalBaselineFactor),
		VolumeReferenceFileName:      stringFromPtr(setting.VolumeReferenceFileName),
		SchemaCheckEnabled:           boolFromPtr(setting.SchemaCheckEnabled),
		SchemaCheck:                  types.ObjectNull(schemaCheckObjectType().AttrTypes),
	}

	if len(setting.GroupKeys) > 0 {
		groups := make(map[string]groupFieldsModel, len(setting.GroupKeys))
		for group, fields := range setting.GroupKeys {
			groups[group] = groupFieldsModel{Fields: fieldSettingsFromClient(fields)}
		}
		m.GroupKeys, d = groupFieldsToMap(ctx, groups)
		diags.Append(d...)
		if diags.HasError() {
			return nil, diags
		}
	}

	if setting.VolumeThresholds != nil {
		m.VolumeThresholds = &volumeThresholdsModel{
			Warning:  float64FromPtr(setting.VolumeThresholds.Warning),
			Critical: float64FromPtr(setting.VolumeThresholds.Critical),
		}
	}

	if setting.VolumeReference != nil {
		ref := &volumeReferenceModel{}
		for _, h := range setting.VolumeReference.Holidays {
			ref.Holidays = append(ref.Holidays, volumeHolidayModel{
				Name:  types.StringValue(h.Name),
				Start: types.StringValue(h.Start),
				End:   types.StringValue(h.End),
			})
		}
		for _, v := range setting.VolumeReference.Volumes {
			ref.Volumes = append(ref.Volumes, volumeEntryModel{
				Group:     stringFromPtr(v.Group),
				Date:      stringFromPtr(v.Date),
				DayOfWeek: stringFromPtr(v.DayOfWeek),
				Holiday:   stringFromPtr(v.Holiday),
				Volume:    float64FromPtr(v.Volume),
			})
		}
		m.VolumeReference = ref
	}

	if setting.SchemaCheck != nil {
		obj, d := schemaCheckObject(ctx, setting.SchemaCheck)
		diags.Append(d...)
		if diags.HasError() {
			return nil, diags
		}
		m.SchemaCheck = obj
	}

	return m, diags
}

func fieldSettingsFromClient(fields map[string]client.DataQualityFieldSetting) map[string]fieldSettingModel {
	if len(fields) == 0 {
		return nil
	}

	out := make(map[string]fieldSettingModel, len(fields))
	for name, f := range fields {
		model := fieldSettingModel{
			FieldType:    stringFromPtr(f.FieldType),
			CustomRegex:  stringFromPtr(f.CustomRegex),
			CustomName:   stringFromPtr(f.CustomName),
			Threshold:    stringFromPtr(f.Threshold),
			Order:        int64FromPtr(f.Order),
			IsPrimaryKey: boolFromPtr(f.IsPrimaryKey),
		}
		for _, t := range f.DataQualityTypes {
			model.DataQualityTypes = append(model.DataQualityTypes, types.StringValue(t))
		}
		if len(f.RegexConfig) > 0 {
			model.RegexConfig = make(map[string]regexSettingModel, len(f.RegexConfig))
			for check, rc := range f.RegexConfig {
				model.RegexConfig[check] = regexSettingModel{
					ConditionRegex:   stringValues(rc.ConditionRegex),
					DataFillingRegex: stringValues(rc.DataFillingRegex),
				}
			}
		}
		for _, ck := range f.ConsistencyKeys {
			model.ConsistencyKeys = append(model.ConsistencyKeys, consistencyKeyModel{
				Key:   types.StringValue(ck.Key),
				Value: stringFromPtr(ck.Value),
			})
		}
		if len(f.DataQualityDescriptions) > 0 {
			model.DataQualityDescriptions = make(map[string]types.String, len(f.DataQualityDescriptions))
			for check, text := range f.DataQualityDescriptions {
				model.DataQualityDescriptions[check] = types.StringValue(text)
			}
		}
		out[name] = model
	}
	return out
}

func schemaCheckObject(ctx context.Context, sc *client.SchemaCheck) (types.Object, diag.Diagnostics) {
	return types.ObjectValueFrom(ctx, schemaCheckObjectType().AttrTypes, schemaCheckModel{
		FieldOrder:       boolFromPtr(sc.FieldOrder),
		UnexpectedFields: boolFromPtr(sc.UnexpectedFields),
		LostFields:       boolFromPtr(sc.LostFields),
	})
}

// fillComputedDataQuality resolves the attributes the backend decides. Only values the plan left
// unknown are written: everything else the plan already settled, and a write has to return it
// unchanged.
func fillComputedDataQuality(ctx context.Context, planned *dataQualityModel, actual *client.DataQualitySetting) diag.Diagnostics {
	var diags diag.Diagnostics

	if planned == nil {
		return diags
	}
	if actual == nil {
		clearUnknownDataQuality(planned)
		return diags
	}

	if planned.VolumeDetectionMode.IsUnknown() {
		planned.VolumeDetectionMode = stringFromPtr(actual.VolumeDetectionMode)
	}
	if planned.VolumeSensitivity.IsUnknown() {
		planned.VolumeSensitivity = stringFromPtr(actual.VolumeSensitivity)
	}
	if planned.VolumeWarningBaselineFactor.IsUnknown() {
		planned.VolumeWarningBaselineFactor = float64FromPtr(actual.VolumeWarningBaselineFactor)
	}
	if planned.VolumeCriticalBaselineFactor.IsUnknown() {
		planned.VolumeCriticalBaselineFactor = float64FromPtr(actual.VolumeCriticalBaselineFactor)
	}

	if planned.SchemaCheck.IsUnknown() {
		if actual.SchemaCheck == nil {
			planned.SchemaCheck = types.ObjectNull(schemaCheckObjectType().AttrTypes)
			return diags
		}
		obj, d := schemaCheckObject(ctx, actual.SchemaCheck)
		diags.Append(d...)
		if diags.HasError() {
			return diags
		}
		planned.SchemaCheck = obj
		return diags
	}

	if planned.SchemaCheck.IsNull() || actual.SchemaCheck == nil {
		return diags
	}

	var sc schemaCheckModel
	diags.Append(planned.SchemaCheck.As(ctx, &sc, basetypes.ObjectAsOptions{})...)
	if diags.HasError() {
		return diags
	}
	if sc.FieldOrder.IsUnknown() {
		sc.FieldOrder = boolFromPtr(actual.SchemaCheck.FieldOrder)
	}
	if sc.UnexpectedFields.IsUnknown() {
		sc.UnexpectedFields = boolFromPtr(actual.SchemaCheck.UnexpectedFields)
	}
	if sc.LostFields.IsUnknown() {
		sc.LostFields = boolFromPtr(actual.SchemaCheck.LostFields)
	}
	obj, d := types.ObjectValueFrom(ctx, schemaCheckObjectType().AttrTypes, sc)
	diags.Append(d...)
	if diags.HasError() {
		return diags
	}
	planned.SchemaCheck = obj

	return diags
}

// clearUnknownDataQuality settles every computed attribute as null, for the paths that have to
// write state without having read the monitor back.
func clearUnknownDataQuality(m *dataQualityModel) {
	if m.Keys.IsUnknown() {
		m.Keys = types.MapNull(fieldSettingObjectType())
	}
	if m.GroupKeys.IsUnknown() {
		m.GroupKeys = types.MapNull(groupFieldsObjectType())
	}
	if m.VolumeDetectionMode.IsUnknown() {
		m.VolumeDetectionMode = types.StringNull()
	}
	if m.VolumeSensitivity.IsUnknown() {
		m.VolumeSensitivity = types.StringNull()
	}
	if m.VolumeWarningBaselineFactor.IsUnknown() {
		m.VolumeWarningBaselineFactor = types.Float64Null()
	}
	if m.VolumeCriticalBaselineFactor.IsUnknown() {
		m.VolumeCriticalBaselineFactor = types.Float64Null()
	}
	if m.SchemaCheck.IsUnknown() {
		m.SchemaCheck = types.ObjectNull(schemaCheckObjectType().AttrTypes)
	}
}

func stringPtr(v types.String) *string {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	s := v.ValueString()
	return &s
}

func boolPtr(v types.Bool) *bool {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	b := v.ValueBool()
	return &b
}

func float64Ptr(v types.Float64) *float64 {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	f := v.ValueFloat64()
	return &f
}

func int64Ptr(v types.Int64) *int64 {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	i := v.ValueInt64()
	return &i
}

func stringFromPtr(v *string) types.String {
	if v == nil {
		return types.StringNull()
	}
	return types.StringValue(*v)
}

func boolFromPtr(v *bool) types.Bool {
	if v == nil {
		return types.BoolNull()
	}
	return types.BoolValue(*v)
}

func float64FromPtr(v *float64) types.Float64 {
	if v == nil {
		return types.Float64Null()
	}
	return types.Float64Value(*v)
}

func int64FromPtr(v *int64) types.Int64 {
	if v == nil {
		return types.Int64Null()
	}
	return types.Int64Value(*v)
}

func stringSlice(values []types.String) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, v := range values {
		out = append(out, v.ValueString())
	}
	return out
}

func stringValues(values []string) []types.String {
	if len(values) == 0 {
		return nil
	}
	out := make([]types.String, 0, len(values))
	for _, v := range values {
		out = append(out, types.StringValue(v))
	}
	return out
}

func fieldSettingsFromMap(ctx context.Context, m types.Map) (map[string]fieldSettingModel, diag.Diagnostics) {
	if m.IsNull() || m.IsUnknown() {
		return nil, nil
	}
	out := map[string]fieldSettingModel{}
	diags := m.ElementsAs(ctx, &out, false)
	return out, diags
}

func fieldSettingsToMap(ctx context.Context, fields map[string]fieldSettingModel) (types.Map, diag.Diagnostics) {
	if len(fields) == 0 {
		return types.MapNull(fieldSettingObjectType()), nil
	}
	return types.MapValueFrom(ctx, fieldSettingObjectType(), fields)
}

func groupFieldsFromMap(ctx context.Context, m types.Map) (map[string]groupFieldsModel, diag.Diagnostics) {
	if m.IsNull() || m.IsUnknown() {
		return nil, nil
	}
	out := map[string]groupFieldsModel{}
	diags := m.ElementsAs(ctx, &out, false)
	return out, diags
}

func groupFieldsToMap(ctx context.Context, groups map[string]groupFieldsModel) (types.Map, diag.Diagnostics) {
	if len(groups) == 0 {
		return types.MapNull(groupFieldsObjectType()), nil
	}
	return types.MapValueFrom(ctx, groupFieldsObjectType(), groups)
}

// uploadToModel expands upload_json into the planned keys and group_keys.
func uploadToModel(ctx context.Context, raw, groupNameKey, groupExtractionRegex string) (keys, groupKeys types.Map, warnings []string, diags diag.Diagnostics) {
	expansion, err := expandDataQualityUpload(raw, groupNameKey, groupExtractionRegex)
	if err != nil {
		diags.AddAttributeError(path.Root("data_quality").AtName("upload_json"), "Invalid Upload JSON", err.Error())
		return types.MapNull(fieldSettingObjectType()), types.MapNull(groupFieldsObjectType()), nil, diags
	}

	keys, d := fieldSettingsToMap(ctx, fieldSettingsFromClient(expansion.Keys))
	diags.Append(d...)

	groups := make(map[string]groupFieldsModel, len(expansion.GroupKeys))
	for group, fields := range expansion.GroupKeys {
		groups[group] = groupFieldsModel{Fields: fieldSettingsFromClient(fields)}
	}
	groupKeys, d = groupFieldsToMap(ctx, groups)
	diags.Append(d...)

	return keys, groupKeys, expansion.Warnings, diags
}

// resolveUploadAtApply expands upload_json when the plan could not, because the file, the group key
// or the extraction regex was only known at apply time.
func resolveUploadAtApply(ctx context.Context, m *dataQualityModel) diag.Diagnostics {
	if m == nil || m.UploadJSON.IsNull() || m.UploadJSON.IsUnknown() {
		return nil
	}
	if !m.Keys.IsUnknown() && !m.GroupKeys.IsUnknown() {
		return nil
	}
	keys, groupKeys, _, diags := uploadToModel(ctx, m.UploadJSON.ValueString(), knownString(m.GroupNameKey), knownString(m.GroupExtractionRegex))
	if diags.HasError() {
		return diags
	}
	m.Keys, m.GroupKeys = keys, groupKeys
	return diags
}
