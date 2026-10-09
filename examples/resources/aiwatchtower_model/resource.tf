# Copyright (c) InsightFinder Inc.
# SPDX-License-Identifier: Apache-2.0

# Minimal model: no data sources, nothing provisioned in UIE.
resource "aiwatchtower_model" "basic" {
  model_name    = "Fraud-Guard"
  model_version = "V1.0.0"
  model_type    = "OpenAI"
}

# Model with data sources. Each entry makes AI WatchTower create a UIE project named
# "<model_name>-<model_version>-<name>", so "name" must be a SHORT LABEL, not an
# existing project name — passing a full project name produces a doubled prefix
# (Fraud-Guard-V1-0-0-Fraud-Guard-V1-0-0-OutputLog) that resolves to nothing.
resource "aiwatchtower_model" "full" {
  model_name    = "Fraud-Guard"
  model_version = "V2.0.0"
  model_type    = "OpenAI"

  environment = "Production"
  description = "Fraud detection model, second generation"

  data_sources = [
    {
      name        = "InputLog"
      data_type   = "Log"
      data_source = "Custom"
    },
    {
      name        = "OutputLog"
      data_type   = "Log"
      data_source = "Custom"
    },
  ]

  ubl_dimension = 128
}

output "model_id" {
  value = aiwatchtower_model.full.model_id
}

# Monitors attached to the model are read-only here; they are managed outside this resource.
output "attached_monitors" {
  value = aiwatchtower_model.full.monitor_info
}
