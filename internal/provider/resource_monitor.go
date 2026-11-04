// Copyright (c) InsightFinder Inc.
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/insightfinder/terraform-provider-aiwatchtower/internal/provider/client"
)

var (
	_ resource.Resource                   = &monitorResource{}
	_ resource.ResourceWithConfigure      = &monitorResource{}
	_ resource.ResourceWithImportState    = &monitorResource{}
	_ resource.ResourceWithValidateConfig = &monitorResource{}
)

func NewMonitorResource() resource.Resource {
	return &monitorResource{}
}

type monitorResource struct {
	client *client.Client
}

type monitorScopeModel struct {
	Type    types.String `tfsdk:"type"`
	Keyword types.String `tfsdk:"keyword"`
	Include types.Bool   `tfsdk:"include"`
}

type monitorResourceModel struct {
	ID           types.String         `tfsdk:"id"`
	UserName     types.String         `tfsdk:"user_name"`
	MonitorName  types.String         `tfsdk:"monitor_name"`
	MonitorType  types.String         `tfsdk:"monitor_type"`
	ModelName    types.String         `tfsdk:"model_name"`
	ModelVersion types.String         `tfsdk:"model_version"`
	Environment  types.String         `tfsdk:"environment"`
	Description  types.String         `tfsdk:"description"`
	DataSources  types.Set            `tfsdk:"data_sources"`
	MonitorScope []monitorScopeModel  `tfsdk:"monitor_scope"`
	DataQuality  *dataQualityModel    `tfsdk:"data_quality"`
	SettingJSON  jsontypes.Normalized `tfsdk:"monitor_setting_json"`
}

func (r *monitorResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_monitor"
}

func (r *monitorResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages an AI WatchTower monitor. A monitor is identified by the (user_name, monitor_name) pair and watches one model's data sources for a single monitor type. " +
			"Only description, monitor_scope and the monitor setting can be changed in place; every other attribute replaces the monitor.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Identifier for the monitor (user_name@monitor_name).",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"user_name": schema.StringAttribute{
				Description: "Owning AI WatchTower user. Defaults to the provider's username.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"monitor_name": schema.StringAttribute{
				Description: "Name of the monitor, unique per user.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"monitor_type": schema.StringAttribute{
				Description: "Monitor type. One of DATA_DRIFT, DATA_NOISE, DATA_QUALITY, LLM_PERFORMANCE, LLM_TRUST_AND_SAFETY, MODEL_DRIFT, MODEL_PERFORMANCE, MODEL_BIAS, MODEL_AVAILABILITY. " +
					"A model may carry at most one monitor of each type.",
				Required: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"model_name": schema.StringAttribute{
				Description: "Name of the model this monitor watches. The model must already exist. Omit it only for DATA_NOISE, where the backend fills it in from the targeted InsightFinder system.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"model_version": schema.StringAttribute{
				Description: "Version of the model this monitor watches. Omit it only for DATA_NOISE.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"environment": schema.StringAttribute{
				Description: "Environment label for the monitor. When left unset the backend reports the model's own environment, which is what this attribute then holds.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"description": schema.StringAttribute{
				Description: "Free-form description of the monitor.",
				Optional:    true,
			},
			"data_sources": schema.SetNestedAttribute{
				Description: "Data sources of the model this monitor applies to. Required for every monitor type except DATA_NOISE. " +
					"The backend silently ignores data sources whose type the monitor cannot use — a Metric data source for every type except MODEL_PERFORMANCE, and anything but Trace for LLM_TRUST_AND_SAFETY — so this provider fails the apply instead of leaving state that claims more than was stored.",
				Optional: true,
				PlanModifiers: []planmodifier.Set{
					setplanmodifier.RequiresReplace(),
				},
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"name": schema.StringAttribute{
							Description: "Short name of the data source, as it appears on the model.",
							Required:    true,
						},
						"data_type": schema.StringAttribute{
							Description: "Project data type, for example 'Log', 'Metric' or 'Trace'.",
							Required:    true,
						},
						"data_source": schema.StringAttribute{
							Description: "Cloud/source type for the project, for example 'Custom'.",
							Optional:    true,
						},
					},
				},
			},
			"monitor_scope": schema.ListNestedAttribute{
				Description: "Keyword filters narrowing what the monitor looks at. Ordered, like the list the backend stores.",
				Optional:    true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"type": schema.StringAttribute{
							Description: "What the keyword is matched against, for example a field name.",
							Required:    true,
						},
						"keyword": schema.StringAttribute{
							Description: "The keyword to match.",
							Required:    true,
						},
						"include": schema.BoolAttribute{
							Description: "Whether matches are included (true) or excluded (false).",
							Required:    true,
						},
					},
				},
			},
			"data_quality": dataQualitySchema(),
			"monitor_setting_json": schema.StringAttribute{
				Description: "Monitor setting as raw JSON, for the monitor types this provider does not yet model as typed attributes. Must carry the discriminating \"type\" property, for example {\"type\":\"MODEL_DRIFT\",...}. " +
					"Mutually exclusive with data_quality, which is the only supported way to configure a DATA_QUALITY monitor. " +
					"Note that the backend normalizes the settings of some monitor types on write, so a value that is not already normalized shows a permanent diff; use lifecycle.ignore_changes in that case.",
				Optional:   true,
				CustomType: jsontypes.NormalizedType{},
			},
		},
	}
}

func (r *monitorResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *client.Client, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)
		return
	}

	r.client = c
}

// Attributes are read one at a time rather than into the resource model: a configuration may hold
// an unknown anywhere, and reflecting an unknown into the model's pointer and slice fields fails.
func (r *monitorResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var monitorTypeValue, modelName, modelVersion types.String
	var settingJSON jsontypes.Normalized
	var dataQuality types.Object
	var dataSources types.Set

	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("monitor_type"), &monitorTypeValue)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("monitor_setting_json"), &settingJSON)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("data_quality"), &dataQuality)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("model_name"), &modelName)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("model_version"), &modelVersion)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("data_sources"), &dataSources)...)
	if resp.Diagnostics.HasError() {
		return
	}

	hasDataQuality := !dataQuality.IsNull()
	hasJSON := !settingJSON.IsNull()

	if hasDataQuality && hasJSON {
		resp.Diagnostics.AddError(
			"Conflicting Monitor Setting",
			"Only one of data_quality and monitor_setting_json may be set. data_quality is the typed form of a DATA_QUALITY setting; "+
				"monitor_setting_json carries the setting of the other monitor types verbatim.",
		)
		return
	}

	monitorType := monitorTypeValue.ValueString()
	if monitorTypeValue.IsUnknown() || monitorType == "" {
		return
	}

	switch {
	case monitorType == client.MonitorTypeDataQuality && hasJSON:
		resp.Diagnostics.AddAttributeError(
			path.Root("monitor_setting_json"),
			"Wrong Monitor Setting Form",
			"A DATA_QUALITY monitor must be configured through the data_quality attribute. The backend fills in defaults for an omitted "+
				"volume or schema check setting, which this provider can only reconcile through the typed attributes.",
		)
	case monitorType != client.MonitorTypeDataQuality && hasDataQuality:
		resp.Diagnostics.AddAttributeError(
			path.Root("data_quality"),
			"Wrong Monitor Setting Form",
			fmt.Sprintf("data_quality configures a DATA_QUALITY monitor, but monitor_type is %q. Use monitor_setting_json for that type.", monitorType),
		)
	}

	if monitorType == monitorTypeDataNoise {
		return
	}

	if modelName.IsNull() || modelVersion.IsNull() {
		resp.Diagnostics.AddError(
			"Missing Model Reference",
			fmt.Sprintf("model_name and model_version are required for a %s monitor; only DATA_NOISE derives them from its setting.", monitorType),
		)
	}
	if dataSources.IsNull() {
		resp.Diagnostics.AddAttributeError(
			path.Root("data_sources"),
			"Missing Data Sources",
			fmt.Sprintf("At least one data source is required for a %s monitor.", monitorType),
		)
	}
}

const monitorTypeDataNoise = "DATA_NOISE"

func (r *monitorResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan monitorResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	userName := plan.UserName.ValueString()
	if plan.UserName.IsNull() || plan.UserName.IsUnknown() || userName == "" {
		userName = r.client.Username
		plan.UserName = types.StringValue(userName)
	}

	config, diags := r.buildMonitorConfig(ctx, plan, userName)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Debug(ctx, "Creating AI WatchTower monitor", map[string]interface{}{
		"monitor_name": config.MonitorName,
		"monitor_type": config.MonitorType,
	})

	if err := r.client.CreateMonitor(ctx, config); err != nil {
		resp.Diagnostics.AddError("Error Creating Monitor", "Could not create monitor: "+err.Error())
		return
	}

	plan.ID = types.StringValue(monitorID(userName, config.MonitorName))
	r.finishWrite(ctx, &plan, config, userName, &resp.Diagnostics, &resp.State)
}

func (r *monitorResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state monitorResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	userName := state.UserName.ValueString()
	if userName == "" {
		userName = r.client.Username
	}

	config, err := r.client.GetMonitor(ctx, userName, state.MonitorName.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error Reading Monitor", "Could not read monitor: "+err.Error())
		return
	}
	if config == nil {
		resp.State.RemoveResource(ctx)
		return
	}

	usedJSON := !state.SettingJSON.IsNull()
	if diags := r.applyConfigToModel(ctx, &state, config, usedJSON); diags.HasError() {
		resp.Diagnostics.Append(diags...)
		return
	}
	state.UserName = types.StringValue(userName)
	state.ID = types.StringValue(monitorID(userName, config.MonitorName))

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *monitorResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state monitorResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	userName := state.UserName.ValueString()
	if userName == "" {
		userName = r.client.Username
	}
	plan.UserName = types.StringValue(userName)

	config, diags := r.buildMonitorConfig(ctx, plan, userName)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Debug(ctx, "Updating AI WatchTower monitor", map[string]interface{}{
		"monitor_name": config.MonitorName,
	})

	if err := r.client.UpdateMonitor(ctx, config); err != nil {
		resp.Diagnostics.AddError("Error Updating Monitor", "Could not update monitor: "+err.Error())
		return
	}

	plan.ID = types.StringValue(monitorID(userName, config.MonitorName))
	r.finishWrite(ctx, &plan, config, userName, &resp.Diagnostics, &resp.State)
}

func (r *monitorResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state monitorResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	userName := state.UserName.ValueString()
	if userName == "" {
		userName = r.client.Username
	}

	tflog.Debug(ctx, "Deleting AI WatchTower monitor", map[string]interface{}{
		"monitor_name": state.MonitorName.ValueString(),
	})

	if err := r.client.DeleteMonitor(ctx, userName, state.MonitorName.ValueString()); err != nil {
		resp.Diagnostics.AddError("Error Deleting Monitor", "Could not delete monitor: "+err.Error())
		return
	}
}

func (r *monitorResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	userName, monitorName, err := parseMonitorImportID(req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Import ID", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("user_name"), userName)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("monitor_name"), monitorName)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}

func parseMonitorImportID(id string) (userName, monitorName string, err error) {
	const format = "import ID must be in the format user_name@monitor_name, " +
		"for example user@company.com@Fraud-Guard-Data-Quality"

	sep := strings.LastIndex(id, "@")
	if sep <= 0 || sep == len(id)-1 {
		return "", "", errors.New(format)
	}
	return id[:sep], id[sep+1:], nil
}

func monitorID(userName, monitorName string) string {
	return fmt.Sprintf("%s@%s", userName, monitorName)
}

// finishWrite reads the monitor back, fills in what the backend computed, reports any data source
// the backend dropped, and writes state. State is written even when the read-back fails so a
// monitor that was created is never left out of state.
func (r *monitorResource) finishWrite(ctx context.Context, plan *monitorResourceModel, sent *client.MonitorConfig,
	userName string, diags *diag.Diagnostics, state *tfsdk.State) {

	actual, err := r.readBackMonitor(ctx, userName, sent.MonitorName)
	if err != nil {
		setMonitorComputedAsEmpty(plan)
		diags.Append(state.Set(ctx, plan)...)
		diags.AddError("Error Reading Monitor", "Could not read the monitor back after writing it: "+err.Error())
		return
	}
	if actual == nil {
		setMonitorComputedAsEmpty(plan)
		diags.Append(state.Set(ctx, plan)...)
		diags.AddError(
			"Monitor Not Found After Write",
			fmt.Sprintf("Monitor %q could not be read back immediately after being written. The write itself reported success, "+
				"so the monitor most likely exists; run \"terraform plan\" to reconcile.", sent.MonitorName),
		)
		return
	}

	plan.ModelName = stringOrNullKeepKnown(plan.ModelName, actual.ModelName)
	plan.ModelVersion = stringOrNullKeepKnown(plan.ModelVersion, actual.ModelVersion)
	plan.Environment = stringOrNullKeepKnown(plan.Environment, actual.Environment)

	if plan.DataQuality != nil {
		setting, parseErr := client.ParseDataQualitySetting(actual.Setting)
		if parseErr != nil {
			diags.AddError("Error Reading Monitor", "Could not parse the data quality setting the backend returned: "+parseErr.Error())
			return
		}
		diags.Append(fillComputedDataQuality(ctx, plan.DataQuality, setting)...)
		if diags.HasError() {
			return
		}
	}

	if dropped := client.DroppedDataSources(sent.DataSources, actual.DataSources); len(dropped) > 0 {
		sourceDiags := r.applyConfigToModel(ctx, plan, actual, !plan.SettingJSON.IsNull())
		diags.Append(sourceDiags...)
		if !sourceDiags.HasError() {
			plan.UserName = types.StringValue(userName)
			plan.ID = types.StringValue(monitorID(userName, actual.MonitorName))
			diags.Append(state.Set(ctx, plan)...)
		}
		diags.AddError(
			"Data Sources Rejected By The Monitor Type",
			fmt.Sprintf("The backend stored the monitor without these data sources: %s. A %s monitor derives its setting from the data source's "+
				"records, and silently ignores the data source types it cannot read. Remove them from data_sources, or point the monitor at a model "+
				"whose data sources it can use.", strings.Join(dropped, ", "), sent.MonitorType),
		)
		return
	}

	diags.Append(state.Set(ctx, plan)...)
}

func (r *monitorResource) readBackMonitor(ctx context.Context, userName, monitorName string) (*client.MonitorConfig, error) {
	var config *client.MonitorConfig
	var err error

	for attempt := 0; attempt < readBackAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(readBackDelay):
			}
		}

		config, err = r.client.GetMonitor(ctx, userName, monitorName)
		if err != nil || config != nil {
			break
		}
	}

	return config, err
}

func (r *monitorResource) buildMonitorConfig(ctx context.Context, plan monitorResourceModel, userName string) (*client.MonitorConfig, diag.Diagnostics) {
	var diags diag.Diagnostics

	config := &client.MonitorConfig{
		UserName:     userName,
		MonitorName:  plan.MonitorName.ValueString(),
		MonitorType:  plan.MonitorType.ValueString(),
		ModelName:    knownString(plan.ModelName),
		ModelVersion: knownString(plan.ModelVersion),
		Environment:  knownString(plan.Environment),
		Description:  plan.Description.ValueString(),
	}

	if !plan.DataSources.IsNull() && !plan.DataSources.IsUnknown() {
		var models []modelDataSourceModel
		diags.Append(plan.DataSources.ElementsAs(ctx, &models, false)...)
		if diags.HasError() {
			return nil, diags
		}
		for _, m := range models {
			config.DataSources = append(config.DataSources, client.ModelDataSource{
				Name:       m.Name.ValueString(),
				DataType:   m.DataType.ValueString(),
				DataSource: m.DataSource.ValueString(),
			})
		}
	}

	for _, s := range plan.MonitorScope {
		config.MonitorScope = append(config.MonitorScope, client.MonitorScope{
			Type:    s.Type.ValueString(),
			Keyword: s.Keyword.ValueString(),
			Include: s.Include.ValueBool(),
		})
	}

	switch {
	case plan.DataQuality != nil:
		setting, d := dataQualityToClient(ctx, plan.DataQuality)
		diags.Append(d...)
		if diags.HasError() {
			return nil, diags
		}
		raw, err := setting.Marshal()
		if err != nil {
			diags.AddError("Error Building Monitor Setting", "Could not serialize the data quality setting: "+err.Error())
			return nil, diags
		}
		config.Setting = raw
	case !plan.SettingJSON.IsNull() && !plan.SettingJSON.IsUnknown():
		config.Setting = json.RawMessage(plan.SettingJSON.ValueString())
	}

	return config, diags
}

func (r *monitorResource) applyConfigToModel(ctx context.Context, m *monitorResourceModel, config *client.MonitorConfig, usedJSON bool) diag.Diagnostics {
	var diags diag.Diagnostics

	m.MonitorName = types.StringValue(config.MonitorName)
	m.MonitorType = stringOrNull(config.MonitorType)
	m.ModelName = stringOrNull(config.ModelName)
	m.ModelVersion = stringOrNull(config.ModelVersion)
	m.Environment = stringOrNull(config.Environment)
	m.Description = stringOrNull(config.Description)

	if len(config.DataSources) > 0 {
		elems := make([]modelDataSourceModel, 0, len(config.DataSources))
		for _, ds := range config.DataSources {
			elems = append(elems, modelDataSourceModel{
				Name:       types.StringValue(ds.Name),
				DataType:   types.StringValue(ds.DataType),
				DataSource: stringOrNull(ds.DataSource),
			})
		}
		set, d := types.SetValueFrom(ctx, modelDataSourceObjectType(), elems)
		diags.Append(d...)
		if diags.HasError() {
			return diags
		}
		m.DataSources = set
	} else {
		m.DataSources = types.SetNull(modelDataSourceObjectType())
	}

	// The backend reads an absent scope back as an empty list rather than null.
	if len(config.MonitorScope) > 0 {
		scopes := make([]monitorScopeModel, 0, len(config.MonitorScope))
		for _, s := range config.MonitorScope {
			scopes = append(scopes, monitorScopeModel{
				Type:    types.StringValue(s.Type),
				Keyword: types.StringValue(s.Keyword),
				Include: types.BoolValue(s.Include),
			})
		}
		m.MonitorScope = scopes
	} else {
		m.MonitorScope = nil
	}

	switch {
	case usedJSON:
		if len(config.Setting) > 0 && string(config.Setting) != "null" {
			m.SettingJSON = jsontypes.NewNormalizedValue(string(config.Setting))
		} else {
			m.SettingJSON = jsontypes.NewNormalizedNull()
		}
		m.DataQuality = nil
	case config.MonitorType == client.MonitorTypeDataQuality:
		setting, err := client.ParseDataQualitySetting(config.Setting)
		if err != nil {
			diags.AddError("Error Reading Monitor", "Could not parse the data quality setting the backend returned: "+err.Error())
			return diags
		}
		dq, d := dataQualityFromClient(ctx, setting)
		diags.Append(d...)
		if diags.HasError() {
			return diags
		}
		m.DataQuality = dq
		m.SettingJSON = jsontypes.NewNormalizedNull()
	default:
		m.DataQuality = nil
		if len(config.Setting) > 0 && string(config.Setting) != "null" {
			m.SettingJSON = jsontypes.NewNormalizedValue(string(config.Setting))
		} else {
			m.SettingJSON = jsontypes.NewNormalizedNull()
		}
	}

	return diags
}

func setMonitorComputedAsEmpty(m *monitorResourceModel) {
	if m.ModelName.IsUnknown() {
		m.ModelName = types.StringNull()
	}
	if m.ModelVersion.IsUnknown() {
		m.ModelVersion = types.StringNull()
	}
	if m.Environment.IsUnknown() {
		m.Environment = types.StringNull()
	}
	if m.DataQuality != nil {
		clearUnknownDataQuality(m.DataQuality)
	}
}

func knownString(v types.String) string {
	if v.IsNull() || v.IsUnknown() {
		return ""
	}
	return v.ValueString()
}

// stringOrNullKeepKnown fills an attribute the backend computes, leaving a value the plan already
// decided untouched so a write returns exactly what it was asked for.
func stringOrNullKeepKnown(planned types.String, actual string) types.String {
	if !planned.IsUnknown() {
		return planned
	}
	return stringOrNull(actual)
}
