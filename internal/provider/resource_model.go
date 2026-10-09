// Copyright (c) InsightFinder Inc.
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"
	"strings"

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

// Ensure the implementation satisfies the expected interfaces.
var (
	_ resource.Resource                = &modelResource{}
	_ resource.ResourceWithConfigure   = &modelResource{}
	_ resource.ResourceWithImportState = &modelResource{}
)

// NewModelResource is a helper function to simplify the provider implementation.
func NewModelResource() resource.Resource {
	return &modelResource{}
}

// modelResource is the resource implementation.
type modelResource struct {
	client *client.Client
}

// modelDataSourceModel maps a single data source entry.
type modelDataSourceModel struct {
	Name       types.String `tfsdk:"name"`
	DataType   types.String `tfsdk:"data_type"`
	DataSource types.String `tfsdk:"data_source"`
}

// monitorInfoModel maps a single read-only monitor entry.
type monitorInfoModel struct {
	MonitorName types.String `tfsdk:"monitor_name"`
	MonitorType types.String `tfsdk:"monitor_type"`
}

// modelResourceModel maps the resource schema data.
type modelResourceModel struct {
	ID           types.String `tfsdk:"id"`
	UserName     types.String `tfsdk:"user_name"`
	ModelName    types.String `tfsdk:"model_name"`
	ModelVersion types.String `tfsdk:"model_version"`
	ModelType    types.String `tfsdk:"model_type"`
	Environment  types.String `tfsdk:"environment"`
	Description  types.String `tfsdk:"description"`
	DataSources  types.List   `tfsdk:"data_sources"`
	UBLDimension types.Int64  `tfsdk:"ubl_dimension"`
	ModelID      types.String `tfsdk:"model_id"`
	AnomalyCount types.Int64  `tfsdk:"anomaly_count"`
	MonitorInfo  types.List   `tfsdk:"monitor_info"`
}

// Metadata returns the resource type name.
func (r *modelResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_model"
}

// Schema defines the schema for the resource.
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
			"data_sources": schema.ListNestedAttribute{
				Description: "Data sources to provision for this model. Each entry causes AI WatchTower to create a UIE project asynchronously after apply returns.",
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
			"monitor_info": schema.ListNestedAttribute{
				Description: "Monitors attached to this model. Read-only — monitors are managed outside this resource.",
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

// Configure adds the provider configured client to the resource.
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

// Create creates the resource and sets the initial Terraform state.
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

	if err := r.client.CreateModel(config); err != nil {
		resp.Diagnostics.AddError("Error Creating Model", "Could not create model: "+err.Error())
		return
	}

	if len(config.DataSources) > 0 {
		resp.Diagnostics.AddWarning(
			"Data Source Projects Are Provisioned Asynchronously",
			"AI WatchTower enqueues project creation on a message queue and completes it in the background, so the UIE projects backing this model's data sources may not exist yet even though the apply succeeded. "+
				"Allow time for them to appear before creating monitors against them.",
		)
	}

	// Read back so computed attributes (model_id, anomaly_count, monitor_info) are populated
	// from the backend rather than guessed.
	if diags := r.readIntoModel(ctx, &plan, userName); diags.HasError() {
		resp.Diagnostics.Append(diags...)
		return
	}

	plan.ID = types.StringValue(modelID(userName, plan.ModelName.ValueString(), plan.ModelVersion.ValueString()))

	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

// Read refreshes the Terraform state with the latest data.
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

	config, err := r.client.GetModel(userName, state.ModelName.ValueString(), state.ModelVersion.ValueString())
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

// Update updates the resource and sets the updated Terraform state on success.
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

	// The backend treats a non-empty oldModelVersion that differs from modelVersion as a version
	// rename; passing the unchanged current version for an ordinary edit keeps it on the plain
	// update path.
	oldVersion := state.ModelVersion.ValueString()

	tflog.Debug(ctx, "Updating AI WatchTower model", map[string]interface{}{
		"model_name":  plan.ModelName.ValueString(),
		"old_version": oldVersion,
		"new_version": plan.ModelVersion.ValueString(),
	})

	if err := r.client.UpdateModel(config, oldVersion); err != nil {
		resp.Diagnostics.AddError("Error Updating Model", "Could not update model: "+err.Error())
		return
	}

	if diags := r.readIntoModel(ctx, &plan, userName); diags.HasError() {
		resp.Diagnostics.Append(diags...)
		return
	}

	plan.ID = types.StringValue(modelID(userName, plan.ModelName.ValueString(), plan.ModelVersion.ValueString()))

	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

// Delete deletes the model.
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

	if err := r.client.DeleteModel(userName, state.ModelName.ValueString(), state.ModelVersion.ValueString()); err != nil {
		resp.Diagnostics.AddError("Error Deleting Model", "Could not delete model: "+err.Error())
		return
	}
}

// ImportState imports the resource state.
func (r *modelResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// Split from the right: a user name may legitimately contain "@" (it is often an email), so
	// only the final two separators are structural.
	lastSep := strings.LastIndex(req.ID, "@")
	if lastSep <= 0 || lastSep == len(req.ID)-1 {
		resp.Diagnostics.AddError(
			"Invalid Import ID",
			"Import ID must be in the format: user_name@model_name@model_version, for example user@company.com@Fraud-Guard@V1.0.0",
		)
		return
	}
	firstPart := req.ID[:lastSep]
	modelVersion := req.ID[lastSep+1:]

	nameSep := strings.LastIndex(firstPart, "@")
	if nameSep <= 0 || nameSep == len(firstPart)-1 {
		resp.Diagnostics.AddError(
			"Invalid Import ID",
			"Import ID must be in the format: user_name@model_name@model_version, for example user@company.com@Fraud-Guard@V1.0.0",
		)
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("user_name"), firstPart[:nameSep])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("model_name"), firstPart[nameSep+1:])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("model_version"), modelVersion)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}

// modelID builds the resource's synthetic identifier.
func modelID(userName, modelName, modelVersion string) string {
	return fmt.Sprintf("%s@%s@%s", userName, modelName, modelVersion)
}

// buildModelConfig converts a plan into a client.ModelConfig.
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

// readIntoModel refreshes m from the backend after a write.
func (r *modelResource) readIntoModel(ctx context.Context, m *modelResourceModel, userName string) diag.Diagnostics {
	var diags diag.Diagnostics

	config, err := r.client.GetModel(userName, m.ModelName.ValueString(), m.ModelVersion.ValueString())
	if err != nil {
		diags.AddError("Error Reading Model", "Could not read model back after write: "+err.Error())
		return diags
	}
	if config == nil {
		diags.AddError(
			"Model Not Found After Write",
			fmt.Sprintf("The model %q (version %q) could not be read back immediately after being written.",
				m.ModelName.ValueString(), m.ModelVersion.ValueString()),
		)
		return diags
	}

	return r.applyConfigToModel(ctx, m, config)
}

// applyConfigToModel copies backend state onto the Terraform model.
//
// Only computed attributes are overwritten unconditionally. Optional attributes are left alone
// when the backend reports them empty, so an omitted attribute does not flip between null and ""
// on every refresh.
func (r *modelResource) applyConfigToModel(ctx context.Context, m *modelResourceModel, config *client.ModelConfig) diag.Diagnostics {
	var diags diag.Diagnostics

	m.ModelName = types.StringValue(config.ModelName)
	m.ModelVersion = types.StringValue(config.ModelVersion)
	if config.ModelType != "" {
		m.ModelType = types.StringValue(config.ModelType)
	}
	if config.Environment != "" {
		m.Environment = types.StringValue(config.Environment)
	}
	if config.Description != "" {
		m.Description = types.StringValue(config.Description)
	}

	m.ModelID = types.StringValue(config.ModelID)
	m.AnomalyCount = types.Int64Value(config.AnomalyCount)

	if config.UBLDimension != nil {
		m.UBLDimension = types.Int64Value(*config.UBLDimension)
	}

	if len(config.DataSources) > 0 {
		elems := make([]modelDataSourceModel, 0, len(config.DataSources))
		for _, ds := range config.DataSources {
			e := modelDataSourceModel{
				Name:     types.StringValue(ds.Name),
				DataType: types.StringValue(ds.DataType),
			}
			if ds.DataSource != "" {
				e.DataSource = types.StringValue(ds.DataSource)
			} else {
				e.DataSource = types.StringNull()
			}
			elems = append(elems, e)
		}
		list, d := types.ListValueFrom(ctx, modelDataSourceObjectType(), elems)
		diags.Append(d...)
		if diags.HasError() {
			return diags
		}
		m.DataSources = list
	}

	monitors := make([]monitorInfoModel, 0, len(config.MonitorInfo))
	for _, mi := range config.MonitorInfo {
		monitors = append(monitors, monitorInfoModel{
			MonitorName: types.StringValue(mi.MonitorName),
			MonitorType: types.StringValue(mi.MonitorType),
		})
	}
	monitorList, d := types.ListValueFrom(ctx, monitorInfoObjectType(), monitors)
	diags.Append(d...)
	if diags.HasError() {
		return diags
	}
	m.MonitorInfo = monitorList

	return diags
}

// modelDataSourceObjectType mirrors the data_sources nested schema.
func modelDataSourceObjectType() types.ObjectType {
	return types.ObjectType{AttrTypes: map[string]attr.Type{
		"name":        types.StringType,
		"data_type":   types.StringType,
		"data_source": types.StringType,
	}}
}

// monitorInfoObjectType mirrors the monitor_info nested schema.
func monitorInfoObjectType() types.ObjectType {
	return types.ObjectType{AttrTypes: map[string]attr.Type{
		"monitor_name": types.StringType,
		"monitor_type": types.StringType,
	}}
}
