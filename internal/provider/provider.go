// Copyright (c) InsightFinder Inc.
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"os"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/insightfinder/terraform-provider-aiwatchtower/internal/provider/client"
)

// Ensure the implementation satisfies the expected interfaces.
var (
	_ provider.Provider = &aiWatchTowerProvider{}
)

// New is a helper function to simplify provider server and testing implementation.
func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &aiWatchTowerProvider{
			version: version,
		}
	}
}

// aiWatchTowerProvider is the provider implementation.
type aiWatchTowerProvider struct {
	// version is set to the provider version on release, "dev" when the provider is built and
	// run locally, and "test" when running acceptance testing.
	version string
}

// aiWatchTowerProviderModel maps provider schema data to a Go type.
type aiWatchTowerProviderModel struct {
	BaseURL  types.String `tfsdk:"base_url"`
	Username types.String `tfsdk:"username"`
	APIKey   types.String `tfsdk:"api_key"`
}

// Metadata returns the provider type name.
func (p *aiWatchTowerProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "aiwatchtower"
	resp.Version = p.version
}

// Schema defines the provider-level schema for configuration data.
func (p *aiWatchTowerProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Interact with the InsightFinder AI WatchTower API.",
		Attributes: map[string]schema.Attribute{
			"base_url": schema.StringAttribute{
				Description: "The base URL for the AI WatchTower API. May also be provided via the AW_BASE_URL environment variable.",
				Optional:    true,
			},
			"username": schema.StringAttribute{
				Description: "The user name for AI WatchTower authentication, sent as the X-User-Name header. May also be provided via the AW_USERNAME environment variable.",
				Optional:    true,
				Sensitive:   true,
			},
			"api_key": schema.StringAttribute{
				Description: "The API key for AI WatchTower authentication, sent as the X-Api-Key header. May also be provided via the AW_API_KEY environment variable.",
				Optional:    true,
				Sensitive:   true,
			},
		},
	}
}

// Configure prepares an AI WatchTower API client for data sources and resources.
func (p *aiWatchTowerProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	tflog.Info(ctx, "Configuring AI WatchTower client")

	var config aiWatchTowerProviderModel
	diags := req.Config.Get(ctx, &config)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	if config.BaseURL.IsUnknown() {
		resp.Diagnostics.AddAttributeError(
			path.Root("base_url"),
			"Unknown AI WatchTower API Base URL",
			"The provider cannot create the AI WatchTower API client as there is an unknown configuration value for the base URL. "+
				"Either target apply the source of the value first, set the value statically in the configuration, or use the AW_BASE_URL environment variable.",
		)
	}

	if config.Username.IsUnknown() {
		resp.Diagnostics.AddAttributeError(
			path.Root("username"),
			"Unknown AI WatchTower API User Name",
			"The provider cannot create the AI WatchTower API client as there is an unknown configuration value for the user name. "+
				"Either target apply the source of the value first, set the value statically in the configuration, or use the AW_USERNAME environment variable.",
		)
	}

	if config.APIKey.IsUnknown() {
		resp.Diagnostics.AddAttributeError(
			path.Root("api_key"),
			"Unknown AI WatchTower API Key",
			"The provider cannot create the AI WatchTower API client as there is an unknown configuration value for the API key. "+
				"Either target apply the source of the value first, set the value statically in the configuration, or use the AW_API_KEY environment variable.",
		)
	}

	if resp.Diagnostics.HasError() {
		return
	}

	baseURL := os.Getenv("AW_BASE_URL")
	username := os.Getenv("AW_USERNAME")
	apiKey := os.Getenv("AW_API_KEY")

	if !config.BaseURL.IsNull() {
		baseURL = config.BaseURL.ValueString()
	}
	if !config.Username.IsNull() {
		username = config.Username.ValueString()
	}
	if !config.APIKey.IsNull() {
		apiKey = config.APIKey.ValueString()
	}

	if baseURL == "" {
		resp.Diagnostics.AddAttributeError(
			path.Root("base_url"),
			"Missing AI WatchTower API Base URL",
			"The provider cannot create the AI WatchTower API client as there is a missing or empty value for the base URL. "+
				"Set the base_url value in the configuration or use the AW_BASE_URL environment variable. "+
				"If either is already set, ensure the value is not empty.",
		)
	}

	if username == "" {
		resp.Diagnostics.AddAttributeError(
			path.Root("username"),
			"Missing AI WatchTower API User Name",
			"The provider cannot create the AI WatchTower API client as there is a missing or empty value for the user name. "+
				"Set the username value in the configuration or use the AW_USERNAME environment variable. "+
				"If either is already set, ensure the value is not empty.",
		)
	}

	if apiKey == "" {
		resp.Diagnostics.AddAttributeError(
			path.Root("api_key"),
			"Missing AI WatchTower API Key",
			"The provider cannot create the AI WatchTower API client as there is a missing or empty value for the API key. "+
				"Set the api_key value in the configuration or use the AW_API_KEY environment variable. "+
				"If either is already set, ensure the value is not empty.",
		)
	}

	if resp.Diagnostics.HasError() {
		return
	}

	ctx = tflog.SetField(ctx, "aiwatchtower_base_url", baseURL)
	ctx = tflog.SetField(ctx, "aiwatchtower_username", username)
	ctx = tflog.MaskFieldValuesWithFieldKeys(ctx, "aiwatchtower_api_key")

	tflog.Debug(ctx, "Creating AI WatchTower client")

	c, err := client.NewClient(baseURL, username, apiKey)
	if err != nil {
		resp.Diagnostics.AddError(
			"Unable to Create AI WatchTower API Client",
			"An unexpected error occurred when creating the AI WatchTower API client. "+
				"If the error is not clear, please contact the provider developers.\n\n"+
				"AI WatchTower Client Error: "+err.Error(),
		)
		return
	}

	resp.DataSourceData = c
	resp.ResourceData = c

	tflog.Info(ctx, "Configured AI WatchTower client", map[string]any{"success": true})
}

// DataSources defines the data sources implemented in the provider.
func (p *aiWatchTowerProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{}
}

// Resources defines the resources implemented in the provider.
func (p *aiWatchTowerProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewModelResource,
	}
}
