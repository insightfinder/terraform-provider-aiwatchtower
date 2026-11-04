---
page_title: "aiwatchtower_monitor Resource - terraform-provider-aiwatchtower"
subcategory: ""
description: |-
  Manages an AI WatchTower monitor. A monitor is identified by the (user_name, monitor_name) pair and watches one model's data sources for a single monitor type. Only description, monitor_scope and the monitor setting can be changed in place; every other attribute replaces the monitor.
---

# aiwatchtower_monitor (Resource)

Manages an AI WatchTower monitor. A monitor is identified by the (user_name, monitor_name) pair and watches one model's data sources for a single monitor type. Only description, monitor_scope and the monitor setting can be changed in place; every other attribute replaces the monitor.

## Example Usage

```terraform
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
```

<!-- schema generated by tfplugindocs -->
## Schema

### Required

- `monitor_name` (String) Name of the monitor, unique per user.
- `monitor_type` (String) Monitor type. One of DATA_DRIFT, DATA_NOISE, DATA_QUALITY, LLM_PERFORMANCE, LLM_TRUST_AND_SAFETY, MODEL_DRIFT, MODEL_PERFORMANCE, MODEL_BIAS, MODEL_AVAILABILITY. A model may carry at most one monitor of each type.

### Optional

- `data_quality` (Attributes) Settings of a DATA_QUALITY monitor. Mutually exclusive with monitor_setting_json, and the only supported form for this monitor type. (see [below for nested schema](#nestedatt--data_quality))
- `data_sources` (Attributes Set) Data sources of the model this monitor applies to. Required for every monitor type except DATA_NOISE. The backend silently ignores data sources whose type the monitor cannot use — a Metric data source for every type except MODEL_PERFORMANCE, and anything but Trace for LLM_TRUST_AND_SAFETY — so this provider fails the apply instead of leaving state that claims more than was stored. (see [below for nested schema](#nestedatt--data_sources))
- `description` (String) Free-form description of the monitor.
- `environment` (String) Environment label for the monitor. When left unset the backend reports the model's own environment, which is what this attribute then holds.
- `model_name` (String) Name of the model this monitor watches. The model must already exist. Omit it only for DATA_NOISE, where the backend fills it in from the targeted InsightFinder system.
- `model_version` (String) Version of the model this monitor watches. Omit it only for DATA_NOISE.
- `monitor_scope` (Attributes List) Keyword filters narrowing what the monitor looks at. Ordered, like the list the backend stores. (see [below for nested schema](#nestedatt--monitor_scope))
- `monitor_setting_json` (String) Monitor setting as raw JSON, for the monitor types this provider does not yet model as typed attributes. Must carry the discriminating "type" property, for example {"type":"MODEL_DRIFT",...}. Mutually exclusive with data_quality, which is the only supported way to configure a DATA_QUALITY monitor. Note that the backend normalizes the settings of some monitor types on write, so a value that is not already normalized shows a permanent diff; use lifecycle.ignore_changes in that case.
- `user_name` (String) Owning AI WatchTower user. Defaults to the provider's username.

### Read-Only

- `id` (String) Identifier for the monitor (user_name@monitor_name).

<a id="nestedatt--data_quality"></a>
### Nested Schema for `data_quality`

Optional:

- `group_keys` (Attributes Map) Rules that apply only to the events of one group, keyed by the group value. The same field can carry different rules in different groups. (see [below for nested schema](#nestedatt--data_quality--group_keys))
- `group_name_key` (String) JSON key of the log event whose value names the group, for example "merchant". Leave unset to run every rule over all events together.
- `keys` (Attributes Map) Rules that apply to every event regardless of group, keyed by the event's JSON field name. (see [below for nested schema](#nestedatt--data_quality--keys))
- `schema_check` (Attributes) Which schema sub-checks run. The backend fills in every sub-check left unset as false. (see [below for nested schema](#nestedatt--data_quality--schema_check))
- `schema_check_enabled` (Boolean) Whether each record is checked against the expected set and order of fields.
- `volume_critical_baseline_factor` (Number) How far beyond the baseline band AI mode reports a critical anomaly. Defaults to 1.15.
- `volume_detection_enabled` (Boolean) Whether the daily record count is compared against an expected volume.
- `volume_detection_mode` (String) How the volume verdict is reached: AI compares the day against a learned baseline band, MANUAL against the percentages in volume_thresholds. Defaults to AI.
- `volume_reference` (Attributes) Expected daily volumes. Leave unset to judge every day against the learned baseline alone. A day resolves to the first match of: exact date, named holiday, any holiday, weekday, default — and at each of those an entry naming a group beats one that does not. (see [below for nested schema](#nestedatt--data_quality--volume_reference))
- `volume_reference_file_name` (String) Name shown for the reference volumes in the Workbench. Display only.
- `volume_sensitivity` (String) Width of the baseline band AI mode judges against: HIGH, MEDIUM_HIGH, MEDIUM, MEDIUM_LOW or LOW, strictest first. Defaults to MEDIUM_HIGH.
- `volume_thresholds` (Attributes) Percent deviation from the expected volume that MANUAL mode reports on. Ignored in AI mode. (see [below for nested schema](#nestedatt--data_quality--volume_thresholds))
- `volume_warning_baseline_factor` (Number) How far beyond the baseline band AI mode reports a warning. Defaults to 1.08.

<a id="nestedatt--data_quality--group_keys"></a>
### Nested Schema for `data_quality.group_keys`

Required:

- `fields` (Attributes Map) Rules for this group, keyed by the event's JSON field name. (see [below for nested schema](#nestedatt--data_quality--group_keys--fields))

<a id="nestedatt--data_quality--group_keys--fields"></a>
### Nested Schema for `data_quality.group_keys.fields`

Optional:

- `consistency_keys` (Attributes List) Fields whose values must agree for the CONSISTENCY check. Ordered, like the list the backend stores. (see [below for nested schema](#nestedatt--data_quality--group_keys--fields--consistency_keys))
- `custom_name` (String) Display name of the CUSTOM check.
- `custom_regex` (String) Expression backing the CUSTOM check.
- `data_quality_types` (Set of String) Checks to run on the field: CONSISTENCY, MISSING_DATA, DATA_OUTLIERS, FIELD_TYPE_MISMATCH, QUALIFIED_DATA_RANGE, CUSTOM, ANOMALOUS_DATA_VALUE. Unordered, like the set the backend stores.
- `field_type` (String) Type the field's values are read as: STRING, NUMBER or CATEGORY. Required for ANOMALOUS_DATA_VALUE, which the backend accepts on NUMBER fields only.
- `is_primary_key` (Boolean) Whether the field takes part in the primary key. Records that share the same values of every primary key field within a group and period are flagged as duplicates.
- `order` (Number) Position of the field in the Workbench's field table. A single counter across the whole table, not restarted per group.
- `regex_config` (Attributes Map) Per-check regular expressions, keyed by the check name used in data_quality_types. (see [below for nested schema](#nestedatt--data_quality--group_keys--fields--regex_config))
- `threshold` (String) Threshold of the check, as the backend stores it: a string, not a number.

<a id="nestedatt--data_quality--group_keys--fields--consistency_keys"></a>
### Nested Schema for `data_quality.group_keys.fields.consistency_keys`

Required:

- `key` (String) Name of the field to compare against.

Optional:

- `value` (String) Value the field must hold. Leave unset to compare the fields against each other.


<a id="nestedatt--data_quality--group_keys--fields--regex_config"></a>
### Nested Schema for `data_quality.group_keys.fields.regex_config`

Optional:

- `condition_regex` (List of String) Expressions selecting the records the check applies to.
- `data_filling_regex` (List of String) Expressions extracting the value to check out of the record.




<a id="nestedatt--data_quality--keys"></a>
### Nested Schema for `data_quality.keys`

Optional:

- `consistency_keys` (Attributes List) Fields whose values must agree for the CONSISTENCY check. Ordered, like the list the backend stores. (see [below for nested schema](#nestedatt--data_quality--keys--consistency_keys))
- `custom_name` (String) Display name of the CUSTOM check.
- `custom_regex` (String) Expression backing the CUSTOM check.
- `data_quality_types` (Set of String) Checks to run on the field: CONSISTENCY, MISSING_DATA, DATA_OUTLIERS, FIELD_TYPE_MISMATCH, QUALIFIED_DATA_RANGE, CUSTOM, ANOMALOUS_DATA_VALUE. Unordered, like the set the backend stores.
- `field_type` (String) Type the field's values are read as: STRING, NUMBER or CATEGORY. Required for ANOMALOUS_DATA_VALUE, which the backend accepts on NUMBER fields only.
- `is_primary_key` (Boolean) Whether the field takes part in the primary key. Records that share the same values of every primary key field within a group and period are flagged as duplicates.
- `order` (Number) Position of the field in the Workbench's field table. A single counter across the whole table, not restarted per group.
- `regex_config` (Attributes Map) Per-check regular expressions, keyed by the check name used in data_quality_types. (see [below for nested schema](#nestedatt--data_quality--keys--regex_config))
- `threshold` (String) Threshold of the check, as the backend stores it: a string, not a number.

<a id="nestedatt--data_quality--keys--consistency_keys"></a>
### Nested Schema for `data_quality.keys.consistency_keys`

Required:

- `key` (String) Name of the field to compare against.

Optional:

- `value` (String) Value the field must hold. Leave unset to compare the fields against each other.


<a id="nestedatt--data_quality--keys--regex_config"></a>
### Nested Schema for `data_quality.keys.regex_config`

Optional:

- `condition_regex` (List of String) Expressions selecting the records the check applies to.
- `data_filling_regex` (List of String) Expressions extracting the value to check out of the record.



<a id="nestedatt--data_quality--schema_check"></a>
### Nested Schema for `data_quality.schema_check`

Optional:

- `field_order` (Boolean) Report records whose fields arrive in a different order.
- `lost_fields` (Boolean) Report records missing a field that is expected.
- `unexpected_fields` (Boolean) Report records carrying a field that is not expected.


<a id="nestedatt--data_quality--volume_reference"></a>
### Nested Schema for `data_quality.volume_reference`

Optional:

- `holidays` (Attributes List) Recurring date ranges that volume entries can name. Also pushed to the project's holiday calendar so the learned baseline honours them. (see [below for nested schema](#nestedatt--data_quality--volume_reference--holidays))
- `volumes` (Attributes List) Expected record counts. At least one entry is required, and an entry with no selector at all is the default. (see [below for nested schema](#nestedatt--data_quality--volume_reference--volumes))

<a id="nestedatt--data_quality--volume_reference--holidays"></a>
### Nested Schema for `data_quality.volume_reference.holidays`

Required:

- `end` (String) Last day of the range as MM-DD.
- `name` (String) Name of the holiday, referenced by a volume entry. Must be unique, and cannot be "*".
- `start` (String) First day of the range as MM-DD. A range whose start is after its end wraps around the new year.


<a id="nestedatt--data_quality--volume_reference--volumes"></a>
### Nested Schema for `data_quality.volume_reference.volumes`

Optional:

- `date` (String) Single calendar date as YYYY-MM-DD. At most one of date, day_of_week and holiday may be set.
- `day_of_week` (String) Weekday as MONDAY through SUNDAY, upper case — the backend compares the string exactly and does not normalize it.
- `group` (String) Group value this entry applies to. Leave unset to apply it to every group.
- `holiday` (String) Name of a holiday listed in holidays, or "*" for any of them.
- `volume` (Number) Expected number of records for the day. Must not be negative.



<a id="nestedatt--data_quality--volume_thresholds"></a>
### Nested Schema for `data_quality.volume_thresholds`

Optional:

- `critical` (Number) Critical threshold, in percent. Defaults to 40, and must not exceed 100.
- `warning` (Number) Warning threshold, in percent. Defaults to 20, and must be greater than 0 and no greater than critical.



<a id="nestedatt--data_sources"></a>
### Nested Schema for `data_sources`

Required:

- `data_type` (String) Project data type, for example 'Log', 'Metric' or 'Trace'.
- `name` (String) Short name of the data source, as it appears on the model.

Optional:

- `data_source` (String) Cloud/source type for the project, for example 'Custom'.


<a id="nestedatt--monitor_scope"></a>
### Nested Schema for `monitor_scope`

Required:

- `include` (Boolean) Whether matches are included (true) or excluded (false).
- `keyword` (String) The keyword to match.
- `type` (String) What the keyword is matched against, for example a field name.

## Import

Import is supported using the following syntax:

```shell
# Monitors are imported as user_name@monitor_name. The identifier is split from the right, so a
# user name that is an email address imports correctly.
terraform import aiwatchtower_monitor.example 'user@company.com@Fraud-Guard-DQ'
```

## Notes

- **Almost everything replaces the monitor.** The backend's update accepts only a description, a
  scope and a setting. `monitor_name`, `monitor_type`, `model_name`, `model_version`,
  `environment`, `data_sources` and `user_name` therefore force replacement; changing them in
  place is not something the API can express.

- **A model carries at most one monitor of each type.** Creating a second one of the same type
  against the same model is refused with `Monitor type already existed`, and a monitor whose name
  is already taken by the same user is refused as a conflict.

- **Data sources the monitor type cannot read are dropped silently.** Every type except
  `MODEL_PERFORMANCE` rejects a `Metric` data source, and `LLM_TRUST_AND_SAFETY` accepts nothing
  but `Trace`, because the setting is derived from the data source's own records. The backend
  stores the monitor with the remaining data sources and reports success. This provider compares
  the write against the read-back and fails the apply instead, naming what was dropped, so state
  never claims a data source that was not stored.

- **`environment` reflects the model when it is left unset.** The backend falls back to the
  model's own environment when the monitor does not carry one, so this attribute holds that
  resolved value rather than staying null.

- **The backend fills in volume and schema check defaults.** An omitted `volume_detection_mode`
  becomes `AI`, `volume_sensitivity` becomes `MEDIUM_HIGH`, the baseline factors become 1.08 and
  1.15, and every unset `schema_check` sub-check becomes `false` — whether or not volume detection
  or schema checking is enabled. Those attributes are computed, so they settle to what the backend
  decided rather than showing a permanent diff.

- **`volume_reference.volumes[].day_of_week` is compared exactly.** The backend holds a
  normalization routine for it but never calls it, so a lower-case weekday is stored as written
  and then matches nothing. Write `SATURDAY`, not `Saturday`.

- **`monitor_setting_json` is for the other monitor types only.** A `DATA_QUALITY` monitor must use
  `data_quality`: the defaults the backend writes into a data quality setting cannot be reconciled
  against a raw JSON document, so it would diff forever. Some other types normalize their setting
  on write; if one of those shows a permanent diff, pin it with `lifecycle { ignore_changes }`.

- **A monitor that is gone and one you may not see both answer 403.** The provider tells them apart
  by listing the user's monitors: when the list is readable the 403 means the monitor is gone and
  it is removed from state, and when the list is refused as well the error is reported. Deleting a
  monitor that is already gone is not an error.
