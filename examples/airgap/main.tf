# Copyright (c) InsightFinder Inc.
# SPDX-License-Identifier: Apache-2.0

# The configuration scripts/airgap/prepare-bundle.sh packs for an airgapped machine: a model and a
# data quality monitor configured from the Workbench's Upload JSON file. Replace data-quality.json
# with your own file, and adjust the names below, before building the bundle.

terraform {
  required_providers {
    aiwatchtower = { source = "insightfinder/aiwatchtower" }
  }
}

provider "aiwatchtower" {} # credentials come from the AW_* environment variables

resource "aiwatchtower_model" "this" {
  model_name    = "TF-Upload-Test"
  model_version = "v1"
  model_type    = "Regression"
  environment   = "Test"
  ubl_dimension = 32 # the Workbench's default; read by UBL/embedding uploads
  data_sources  = [{ name = "InputLog", data_type = "Log", data_source = "Custom" }]
}

resource "aiwatchtower_monitor" "data_quality" {
  monitor_name  = "TF-Upload-Test-DQ"
  monitor_type  = "DATA_QUALITY"
  model_name    = aiwatchtower_model.this.model_name
  model_version = aiwatchtower_model.this.model_version
  data_sources  = [{ name = "InputLog", data_type = "Log", data_source = "Custom" }]

  # Every schema and volume setting is spelled out: anything left unset takes the backend's default,
  # which is not the Workbench's (volume detection and the schema check are off, the baseline
  # factors are 1.08 / 1.15).
  data_quality = {
    upload_json            = file("${path.module}/data-quality.json")
    group_name_key         = "group"     # the Workbench's "Group by"
    group_extraction_regex = "^([^@]+)@" # the Workbench's Auto Grouping: "group1@field1" -> group1

    # Schema check. The Workbench's defaults are field_order on, the other two off. With grouping,
    # a group expects only its own fields plus the ungrouped ones, so unexpected_fields flags any
    # record that also carries another group's fields.
    schema_check_enabled = true
    schema_check = {
      field_order       = true
      unexpected_fields = true
      lost_fields       = true
    }
  }
}
