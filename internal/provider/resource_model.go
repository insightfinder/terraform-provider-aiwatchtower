// Copyright (c) InsightFinder Inc.
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/insightfinder/terraform-provider-aiwatchtower/internal/provider/client"
)

var (
	_ resource.Resource                = &modelResource{}
	_ resource.ResourceWithConfigure   = &modelResource{}
	_ resource.ResourceWithImportState = &modelResource{}
)

const (
	readBackAttempts = 3
	readBackDelay    = 500 * time.Millisecond
)

func NewModelResource() resource.Resource {
	return &modelResource{}
}

type modelResource struct {
	client *client.Client
}

type modelDataSourceModel struct {
	Name       types.String `tfsdk:"name"`
	DataType   types.String `tfsdk:"data_type"`
	DataSource types.String `tfsdk:"data_source"`
}

type monitorInfoModel struct {
	MonitorName types.String `tfsdk:"monitor_name"`
	MonitorType types.String `tfsdk:"monitor_type"`
}

type modelResourceModel struct {
	ID           types.String `tfsdk:"id"`
	UserName     types.String `tfsdk:"user_name"`
	ModelName    types.String `tfsdk:"model_name"`
	ModelVersion types.String `tfsdk:"model_version"`
	ModelType    types.String `tfsdk:"model_type"`
	Environment  types.String `tfsdk:"environment"`
	Description  types.String `tfsdk:"description"`
	DataSources  types.Set    `tfsdk:"data_sources"`
	UBLDimension types.Int64  `tfsdk:"ubl_dimension"`
	ModelID      types.String `tfsdk:"model_id"`
	AnomalyCount types.Int64  `tfsdk:"anomaly_count"`
	MonitorInfo  types.Set    `tfsdk:"monitor_info"`
}

func (r *modelResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_model"
}

func (r *modelResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages an AI WatchTower model. A model is identified by the (user_name, model_name, model_version) triple.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Identifier for the model (user_name@model_name@model_version).",
				Computed:    true,
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
			"model_name": schema.StringAttribute{
				Description: "Name of the model.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"model_version": schema.StringAttribute{
				Description: "Version of the model. Changing this renames the version in place — the backend moves the model record, its mapping relation and its monitors — rather than replacing the resource.",
				Required:    true,
			},
			"model_type": schema.StringAttribute{
				Description: "Model type identifier (for example 'OpenAI', 'Regression' or 'Classification'). Valid values come from the backend's configurable built-in and custom model type lists.",
				Required:    true,
			},
			"environment": schema.StringAttribute{
				Description: "Environment label for the model, for example 'Production'.",
				Optional:    true,
			},
			"description": schema.StringAttribute{
				Description: "Free-form description of the model.",
				Optional:    true,
			},
			"data_sources": schema.SetNestedAttribute{
				Description: "Data sources to provision for this model. Each entry causes AI WatchTower to create a UIE project asynchronously after apply returns. Unordered: the backend stores these as a set and returns them in an arbitrary order.",
				Optional:    true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"name": schema.StringAttribute{
							Description: "Short name for the data source, used as a *suffix*. The resulting UIE project is named \"<model_name>-<model_version>-<name>\", so pass a short label such as \"OutputLog\" — passing an existing full project name produces a doubled prefix and an unreachable project.",
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
			"ubl_dimension": schema.Int64Attribute{
				Description: "Dimension of the model's UBL (unsupervised baseline learning) setting.",
				Optional:    true,
			},
			"model_id": schema.StringAttribute{
				Description: "Backend-generated model identifier.",
				Computed:    true,
			},
			"anomaly_count": schema.Int64Attribute{
				Description: "Number of anomalies currently recorded against the model.",
				Computed:    true,
			},
			"monitor_info": schema.SetNestedAttribute{
				Description: "Monitors attached to this model. Read-only — monitors are managed outside this resource. Unordered, like the set the backend stores.",
				Computed:    true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"monitor_name": schema.StringAttribute{Computed: true},
						"monitor_type": schema.StringAttribute{Computed: true},
					},
				},
			},
		},
	}
}

func (r *modelResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *modelResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan modelResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	userName := plan.UserName.ValueString()
	if plan.UserName.IsNull() || plan.UserName.IsUnknown() || userName == "" {
		userName = r.client.Username
		plan.UserName = types.StringValue(userName)
	}

	tflog.Debug(ctx, "Creating AI WatchTower model", map[string]interface{}{
		"model_name":    plan.ModelName.ValueString(),
		"model_version": plan.ModelVersion.ValueString(),
	})

	config, diags := r.buildModelConfig(ctx, plan, userName)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.client.CreateModel(ctx, config); err != nil {
		resp.Diagnostics.AddError("Error Creating Model", "Could not create model: "+err.Error())
		return
	}

	if len(config.DataSources) > 0 {
		resp.Diagnostics.Append(asyncProvisioningWarning())
	}

	plan.ID = types.StringValue(modelID(userName, plan.ModelName.ValueString(), plan.ModelVersion.ValueString()))

	if diags := r.refreshComputed(ctx, &plan, userName); diags.HasError() {
		setComputedUnknownAsEmpty(&plan)
		resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
		resp.Diagnostics.Append(diags...)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *modelResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state modelResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	userName := state.UserName.ValueString()
	if userName == "" {
		userName = r.client.Username
	}

	config, err := r.client.GetModel(ctx, userName, state.ModelName.ValueString(), state.ModelVersion.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error Reading Model", "Could not read model: "+err.Error())
		return
	}
	if config == nil {
		resp.State.RemoveResource(ctx)
		return
	}

	if diags := r.applyConfigToModel(ctx, &state, config); diags.HasError() {
		resp.Diagnostics.Append(diags...)
		return
	}
	state.UserName = types.StringValue(userName)
	state.ID = types.StringValue(modelID(userName, config.ModelName, config.ModelVersion))

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *modelResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state modelResourceModel
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

	config, diags := r.buildModelConfig(ctx, plan, userName)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	oldVersion := state.ModelVersion.ValueString()

	known, d := dataSourceNames(ctx, state.DataSources)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}
	markNewDataSources(config, known)

	if anyNewDataSource(config) {
		resp.Diagnostics.Append(asyncProvisioningWarning())
	}

	tflog.Debug(ctx, "Updating AI WatchTower model", map[string]interface{}{
		"model_name":  plan.ModelName.ValueString(),
		"old_version": oldVersion,
		"new_version": plan.ModelVersion.ValueString(),
	})

	if err := r.client.UpdateModel(ctx, config, oldVersion); err != nil {
		resp.Diagnostics.AddError("Error Updating Model", "Could not update model: "+err.Error())
		return
	}

	plan.ID = types.StringValue(modelID(userName, plan.ModelName.ValueString(), plan.ModelVersion.ValueString()))

	if diags := r.refreshComputed(ctx, &plan, userName); diags.HasError() {
		setComputedUnknownAsEmpty(&plan)
		resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
		resp.Diagnostics.Append(diags...)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *modelResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state modelResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	userName := state.UserName.ValueString()
	if userName == "" {
		userName = r.client.Username
	}

	tflog.Debug(ctx, "Deleting AI WatchTower model", map[string]interface{}{
		"model_name":    state.ModelName.ValueString(),
		"model_version": state.ModelVersion.ValueString(),
	})

	if err := r.client.DeleteModel(ctx, userName, state.ModelName.ValueString(), state.ModelVersion.ValueString()); err != nil {
		resp.Diagnostics.AddError("Error Deleting Model", "Could not delete model: "+err.Error())
		return
	}
}

func (r *modelResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	userName, modelName, modelVersion, err := parseModelImportID(req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Import ID", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("user_name"), userName)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("model_name"), modelName)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("model_version"), modelVersion)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}

func parseModelImportID(id string) (userName, modelName, modelVersion string, err error) {
	const format = "import ID must be in the format user_name@model_name@model_version, " +
		"for example user@company.com@Fraud-Guard@V1.0.0"

	versionSep := strings.LastIndex(id, "@")
	if versionSep <= 0 || versionSep == len(id)-1 {
		return "", "", "", errors.New(format)
	}
	head, modelVersion := id[:versionSep], id[versionSep+1:]

	nameSep := strings.LastIndex(head, "@")
	if nameSep <= 0 || nameSep == len(head)-1 {
		return "", "", "", errors.New(format)
	}

	return head[:nameSep], head[nameSep+1:], modelVersion, nil
}

func dataSourceNames(ctx context.Context, list types.Set) (map[string]bool, diag.Diagnostics) {
	var diags diag.Diagnostics
	names := map[string]bool{}
	if list.IsNull() || list.IsUnknown() {
		return names, diags
	}
	var models []modelDataSourceModel
	diags.Append(list.ElementsAs(ctx, &models, false)...)
	if diags.HasError() {
		return names, diags
	}
	for _, m := range models {
		names[m.Name.ValueString()] = true
	}
	return names, diags
}

func markNewDataSources(config *client.ModelConfig, known map[string]bool) {
	for i := range config.DataSources {
		config.DataSources[i].IsNew = !known[config.DataSources[i].Name]
	}
}

func anyNewDataSource(config *client.ModelConfig) bool {
	for _, ds := range config.DataSources {
		if ds.IsNew {
			return true
		}
	}
	return false
}

func asyncProvisioningWarning() diag.Diagnostic {
	return diag.NewWarningDiagnostic(
		"Data Source Projects Are Provisioned Asynchronously",
		"AI WatchTower enqueues project creation on a message queue and completes it in the background, "+
			"so the UIE projects backing this model's data sources may not exist yet even though the apply "+
			"succeeded. Allow time for them to appear before creating monitors against them.",
	)
}

func modelID(userName, modelName, modelVersion string) string {
	return fmt.Sprintf("%s@%s@%s", userName, modelName, modelVersion)
}

func (r *modelResource) buildModelConfig(ctx context.Context, plan modelResourceModel, userName string) (*client.ModelConfig, diag.Diagnostics) {
	var diags diag.Diagnostics

	config := &client.ModelConfig{
		UserName:     userName,
		ModelName:    plan.ModelName.ValueString(),
		ModelVersion: plan.ModelVersion.ValueString(),
		ModelType:    plan.ModelType.ValueString(),
		Environment:  plan.Environment.ValueString(),
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

	if !plan.UBLDimension.IsNull() && !plan.UBLDimension.IsUnknown() {
		d := plan.UBLDimension.ValueInt64()
		config.UBLDimension = &d
	}

	return config, diags
}

func (r *modelResource) refreshComputed(ctx context.Context, m *modelResourceModel, userName string) diag.Diagnostics {
	var diags diag.Diagnostics

	var config *client.ModelConfig
	var err error
	for attempt := 0; attempt < readBackAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				diags.AddError("Error Reading Model", "Cancelled while reading the model back: "+ctx.Err().Error())
				return diags
			case <-time.After(readBackDelay):
			}
		}

		config, err = r.client.GetModel(ctx, userName, m.ModelName.ValueString(), m.ModelVersion.ValueString())
		if err != nil || config != nil {
			break
		}
	}

	if err != nil {
		diags.AddError("Error Reading Model", "Could not read the model back after writing it: "+err.Error())
		return diags
	}
	if config == nil {
		diags.AddError(
			"Model Not Found After Write",
			fmt.Sprintf("Model %q (version %q) could not be read back immediately after being written. "+
				"The write itself reported success, so the model most likely exists; run \"terraform plan\" to reconcile.",
				m.ModelName.ValueString(), m.ModelVersion.ValueString()),
		)
		return diags
	}

	diags.Append(setComputed(ctx, m, config)...)
	return diags
}

func setComputed(ctx context.Context, m *modelResourceModel, config *client.ModelConfig) diag.Diagnostics {
	var diags diag.Diagnostics

	m.ModelID = types.StringValue(config.ModelID)
	m.AnomalyCount = types.Int64Value(config.AnomalyCount)

	monitors := make([]monitorInfoModel, 0, len(config.MonitorInfo))
	for _, mi := range config.MonitorInfo {
		monitors = append(monitors, monitorInfoModel{
			MonitorName: types.StringValue(mi.MonitorName),
			MonitorType: types.StringValue(mi.MonitorType),
		})
	}
	monitorSet, d := types.SetValueFrom(ctx, monitorInfoObjectType(), monitors)
	diags.Append(d...)
	if diags.HasError() {
		return diags
	}
	m.MonitorInfo = monitorSet

	return diags
}

func setComputedUnknownAsEmpty(m *modelResourceModel) {
	m.ModelID = types.StringValue("")
	m.AnomalyCount = types.Int64Value(0)
	m.MonitorInfo = types.SetValueMust(monitorInfoObjectType(), []attr.Value{})
}

func (r *modelResource) applyConfigToModel(ctx context.Context, m *modelResourceModel, config *client.ModelConfig) diag.Diagnostics {
	var diags diag.Diagnostics

	m.ModelName = types.StringValue(config.ModelName)
	m.ModelVersion = types.StringValue(config.ModelVersion)
	m.ModelType = stringOrNull(config.ModelType)
	m.Environment = stringOrNull(config.Environment)
	m.Description = stringOrNull(config.Description)

	if config.UBLDimension != nil {
		m.UBLDimension = types.Int64Value(*config.UBLDimension)
	} else {
		m.UBLDimension = types.Int64Null()
	}

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

	diags.Append(setComputed(ctx, m, config)...)
	return diags
}

func stringOrNull(v string) types.String {
	if v == "" {
		return types.StringNull()
	}
	return types.StringValue(v)
}

func modelDataSourceObjectType() types.ObjectType {
	return types.ObjectType{AttrTypes: map[string]attr.Type{
		"name":        types.StringType,
		"data_type":   types.StringType,
		"data_source": types.StringType,
	}}
}

func monitorInfoObjectType() types.ObjectType {
	return types.ObjectType{AttrTypes: map[string]attr.Type{
		"monitor_name": types.StringType,
		"monitor_type": types.StringType,
	}}
}
