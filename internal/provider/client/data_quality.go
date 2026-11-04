// Copyright (c) InsightFinder Inc.
// SPDX-License-Identifier: Apache-2.0

package client

import "encoding/json"

type DataQualityRegexSetting struct {
	ConditionRegex   []string `json:"conditionRegex,omitempty"`
	DataFillingRegex []string `json:"dataFillingRegex,omitempty"`
}

type ConsistencyKey struct {
	Key   string  `json:"key"`
	Value *string `json:"value,omitempty"`
}

type DataQualityFieldSetting struct {
	FieldType        *string                            `json:"fieldType,omitempty"`
	DataQualityTypes []string                           `json:"dataQualityTypes,omitempty"`
	RegexConfig      map[string]DataQualityRegexSetting `json:"regexConfig,omitempty"`
	CustomRegex      *string                            `json:"customRegex,omitempty"`
	CustomName       *string                            `json:"customName,omitempty"`
	Threshold        *string                            `json:"threshold,omitempty"`
	ConsistencyKeys  []ConsistencyKey                   `json:"consistencyKeys,omitempty"`
	Order            *int64                             `json:"order,omitempty"`
	IsPrimaryKey     *bool                              `json:"isPrimaryKey,omitempty"`
}

type VolumeThresholds struct {
	Warning  *float64 `json:"warning,omitempty"`
	Critical *float64 `json:"critical,omitempty"`
}

type VolumeHolidayEntry struct {
	Name  string `json:"name"`
	Start string `json:"start"`
	End   string `json:"end"`
}

type VolumeEntry struct {
	Group     *string  `json:"group,omitempty"`
	Date      *string  `json:"date,omitempty"`
	DayOfWeek *string  `json:"dayOfWeek,omitempty"`
	Holiday   *string  `json:"holiday,omitempty"`
	Volume    *float64 `json:"volume,omitempty"`
}

type VolumeReference struct {
	Holidays []VolumeHolidayEntry `json:"holidays,omitempty"`
	Volumes  []VolumeEntry        `json:"volumes,omitempty"`
}

type SchemaCheck struct {
	FieldOrder       *bool `json:"fieldOrder,omitempty"`
	UnexpectedFields *bool `json:"unexpectedFields,omitempty"`
	LostFields       *bool `json:"lostFields,omitempty"`
}

type DataQualitySetting struct {
	Type         string                                        `json:"type"`
	GroupNameKey *string                                       `json:"groupNameKey,omitempty"`
	Keys         map[string]DataQualityFieldSetting            `json:"keys,omitempty"`
	GroupKeys    map[string]map[string]DataQualityFieldSetting `json:"groupKeys,omitempty"`

	VolumeDetectionEnabled       *bool             `json:"volumeDetectionEnabled,omitempty"`
	VolumeDetectionMode          *string           `json:"volumeDetectionMode,omitempty"`
	VolumeThresholds             *VolumeThresholds `json:"volumeThresholds,omitempty"`
	VolumeThresholdsEnabled      *bool             `json:"volumeThresholdsEnabled,omitempty"`
	VolumeSensitivity            *string           `json:"volumeSensitivity,omitempty"`
	VolumeWarningBaselineFactor  *float64          `json:"volumeWarningBaselineFactor,omitempty"`
	VolumeCriticalBaselineFactor *float64          `json:"volumeCriticalBaselineFactor,omitempty"`
	VolumeReferenceFileName      *string           `json:"volumeReferenceFileName,omitempty"`
	VolumeReference              *VolumeReference  `json:"volumeReference,omitempty"`

	SchemaCheckEnabled *bool        `json:"schemaCheckEnabled,omitempty"`
	SchemaCheck        *SchemaCheck `json:"schemaCheck,omitempty"`
}

func NewDataQualitySetting() *DataQualitySetting {
	return &DataQualitySetting{Type: MonitorTypeDataQuality}
}

func (s *DataQualitySetting) Marshal() (json.RawMessage, error) {
	s.Type = MonitorTypeDataQuality
	return json.Marshal(s)
}

func ParseDataQualitySetting(raw json.RawMessage) (*DataQualitySetting, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var setting DataQualitySetting
	if err := json.Unmarshal(raw, &setting); err != nil {
		return nil, err
	}
	return &setting, nil
}
