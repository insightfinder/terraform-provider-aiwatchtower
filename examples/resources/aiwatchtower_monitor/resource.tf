# Copyright (c) InsightFinder Inc.
# SPDX-License-Identifier: Apache-2.0

resource "aiwatchtower_model" "fraud_guard" {
  model_name    = "Fraud-Guard"
  model_version = "V1.0.0"
  model_type    = "OpenAI"
  environment   = "Production"

  data_sources = [
    {
      name        = "InputLog"
      data_type   = "Log"
      data_source = "Custom"
    },
  ]
}

# Minimal data quality monitor: one rule, no volume or schema checking.
resource "aiwatchtower_monitor" "basic" {
  monitor_name  = "Fraud-Guard-DQ"
  monitor_type  = "DATA_QUALITY"
  model_name    = aiwatchtower_model.fraud_guard.model_name
  model_version = aiwatchtower_model.fraud_guard.model_version

  data_sources = [
    {
      name        = "InputLog"
      data_type   = "Log"
      data_source = "Custom"
    },
  ]

  data_quality = {
    keys = {
      transaction_id = {
        field_type         = "STRING"
        data_quality_types = ["MISSING_DATA"]
        is_primary_key     = true
        order              = 0
      }
    }
  }
}

# Every part of a data quality setting: grouped rules, volume detection against a reference, and
# schema checking.
resource "aiwatchtower_monitor" "full" {
  monitor_name  = "Fraud-Guard-DQ-Full"
  monitor_type  = "DATA_QUALITY"
  model_name    = aiwatchtower_model.fraud_guard.model_name
  model_version = aiwatchtower_model.fraud_guard.model_version
  description   = "Payment field quality, per merchant"

  # A Metric data source would be dropped by the backend; the provider fails the apply instead of
  # recording one that was never stored.
  data_sources = [
    {
      name        = "InputLog"
      data_type   = "Log"
      data_source = "Custom"
    },
  ]

  monitor_scope = [
    {
      type    = "field"
      keyword = "test-"
      include = false
    },
  ]

  data_quality = {
    group_name_key = "merchant"

    # Rules applied to every event, whatever its group.
    keys = {
      amount = {
        field_type         = "NUMBER"
        data_quality_types = ["MISSING_DATA", "DATA_OUTLIERS", "ANOMALOUS_DATA_VALUE"]
        threshold          = "3"
        order              = 0
      }

      currency = {
        field_type         = "CATEGORY"
        data_quality_types = ["FIELD_TYPE_MISMATCH", "CONSISTENCY"]
        order              = 1

        consistency_keys = [
          {
            key   = "country"
            value = "US"
          },
        ]
      }

      transaction_id = {
        field_type         = "STRING"
        data_quality_types = ["CUSTOM"]
        custom_name        = "UUID shape"
        custom_regex       = "^[0-9a-f-]{36}$"
        is_primary_key     = true
        order              = 2

        regex_config = {
          CUSTOM = {
            condition_regex = ["\"type\"\\s*:\\s*\"payment\""]
          }
        }
      }
    }

    # Rules that apply only within one group; the same field can be checked differently per group.
    group_keys = {
      Walmart = {
        fields = {
          sku = {
            field_type         = "STRING"
            data_quality_types = ["MISSING_DATA"]
            order              = 3
          }
        }
      }
    }

    volume_detection_enabled = true
    volume_detection_mode    = "MANUAL"

    volume_thresholds = {
      warning  = 15
      critical = 35
    }

    volume_reference_file_name = "expected-volumes.csv"

    volume_reference = {
      holidays = [
        {
          name  = "Christmas"
          start = "12-24"
          end   = "12-26"
        },
      ]

      volumes = [
        # No selector at all: the default.
        { volume = 1000000 },
        # Weekdays are compared as exact upper-case strings.
        { day_of_week = "SATURDAY", volume = 600000 },
        { holiday = "Christmas", volume = 200000 },
        { holiday = "*", volume = 400000 },
        { group = "Walmart", date = "2026-11-27", volume = 3000000 },
      ]
    }

    schema_check_enabled = true

    schema_check = {
      field_order       = false
      lost_fields       = true
      unexpected_fields = true
    }
  }
}

# Monitor types this provider does not model as typed attributes take their setting as raw JSON.
resource "aiwatchtower_monitor" "model_drift" {
  monitor_name  = "Fraud-Guard-Model-Drift"
  monitor_type  = "MODEL_DRIFT"
  model_name    = aiwatchtower_model.fraud_guard.model_name
  model_version = aiwatchtower_model.fraud_guard.model_version

  data_sources = [
    {
      name        = "InputLog"
      data_type   = "Log"
      data_source = "Custom"
    },
  ]

  monitor_setting_json = jsonencode({
    type = "MODEL_DRIFT"
  })
}
