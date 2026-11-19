// Copyright (c) InsightFinder Inc.
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/insightfinder/terraform-provider-aiwatchtower/internal/provider/client"
)

// The Workbench's "Upload JSON" file is expanded here exactly as the UI expands it in the browser
// (AI-WatchTower-UI: src/utils/dataCheckTypes.ts, transformDataToTargetFormat / extractGroup /
// serializeDataQualityPayload in src/utils/index.ts). The backend never sees the file itself.
// DESIGN.md §7.10 lists where this deliberately differs from the UI.

const dataCheckTypesKey = "$dataCheckTypes"

const (
	dqFieldString   = "STRING"
	dqFieldNumber   = "NUMBER"
	dqFieldCategory = "CATEGORY"

	dqMissing      = "MISSING_DATA"
	dqOutliers     = "DATA_OUTLIERS"
	dqTypeMismatch = "FIELD_TYPE_MISMATCH"
	dqRange        = "QUALIFIED_DATA_RANGE"
	dqConsistency  = "CONSISTENCY"
	dqCustom       = "CUSTOM"
	dqAnomalous    = "ANOMALOUS_DATA_VALUE"
)

var uploadCheckKeywords = map[string]string{
	"missing":      dqMissing,
	"invalidvalue": dqOutliers,
	"outliers":     dqOutliers,
	"typemismatch": dqTypeMismatch,
	"range":        dqRange,
	"consistency":  dqConsistency,
	"custom":       dqCustom,
	"anomalous":    dqAnomalous,
}

var uploadFieldTypeKeywords = map[string]string{
	"number":      dqFieldNumber,
	"numerical":   dqFieldNumber,
	"string":      dqFieldString,
	"category":    dqFieldCategory,
	"categorical": dqFieldCategory,
}

var uploadAllowedChecks = map[string]map[string]bool{
	dqFieldString:   {dqMissing: true, dqConsistency: true, dqCustom: true},
	dqFieldNumber:   {dqMissing: true, dqOutliers: true, dqTypeMismatch: true, dqRange: true, dqConsistency: true, dqCustom: true, dqAnomalous: true},
	dqFieldCategory: {dqMissing: true, dqOutliers: true, dqTypeMismatch: true, dqConsistency: true, dqCustom: true},
}

var uploadThresholdPattern = regexp.MustCompile(`^(>=|<=|>|<|=|!=)\s*-?\d+(\.\d+)?$`)

func uploadDefaultChecks(fieldType string) []string {
	if fieldType == dqFieldString {
		return []string{dqMissing}
	}
	return []string{dqMissing, dqOutliers, dqTypeMismatch}
}

// uploadExpansion is the setting an upload file stands for, plus every entry the UI would have
// ignored or flagged.
type uploadExpansion struct {
	Keys      map[string]client.DataQualityFieldSetting
	GroupKeys map[string]map[string]client.DataQualityFieldSetting
	Warnings  []string
}

// expandDataQualityUpload turns an upload file into keys and groupKeys. Every field of the sample
// event is sent, in file order, so the schema check knows the whole event; a field gets checks
// only from its $dataCheckTypes entry. groupNameKey and groupExtractionRegex play the UI's
// "Group by" and Auto Grouping: both have to be set for any field to land in groupKeys.
func expandDataQualityUpload(raw, groupNameKey, groupExtractionRegex string) (*uploadExpansion, error) {
	doc, err := parseOrderedJSON(raw)
	if err != nil {
		return nil, err
	}
	if doc.kind != jsonObject {
		return nil, errors.New("the upload file must be a JSON object: a sample event, optionally with a top-level \"$dataCheckTypes\" object")
	}

	var groupPattern *regexp.Regexp
	if source := jsTrim(groupExtractionRegex); source != "" {
		groupPattern, err = regexp.Compile(source)
		if err != nil {
			return nil, fmt.Errorf("group_extraction_regex %q does not compile: %v. The provider evaluates it with Go's RE2 syntax, which lacks "+
				"some JavaScript features the Workbench accepts, such as lookbehind and backreferences", source, err)
		}
	}

	out := &uploadExpansion{}
	warn := func(field, format string, args ...any) {
		out.Warnings = append(out.Warnings, fmt.Sprintf("%s: %s", field, fmt.Sprintf(format, args...)))
	}

	fields := flattenSampleEvent(doc)
	fieldKeys := make(map[string]bool, len(fields))
	for _, f := range fields {
		fieldKeys[f.key] = true
	}

	config := map[string]*jsonValue{}
	var configOrder []string
	if cfg, ok := doc.member(dataCheckTypesKey); ok {
		if cfg.kind != jsonObject {
			warn(dataCheckTypesKey, "is not an object; every check in it is ignored")
		} else {
			for i := range cfg.members {
				m := &cfg.members[i]
				config[m.key] = &m.value
				configOrder = append(configOrder, m.key)
			}
		}
	}

	grouping := groupNameKey != ""
	seen := map[string]bool{}

	for order, f := range fields {
		setting := uploadFieldSetting(f, config, fieldKeys, warn)
		setting.Order = int64Value(int64(order))

		fieldKey := jsTrim(f.key)
		if fieldKey == "" {
			continue
		}
		if seen[fieldKey] {
			warn(fieldKey, "appears more than once in the sample event; the last occurrence wins")
		}
		seen[fieldKey] = true

		group := ""
		if groupPattern != nil {
			// As in the Workbench, the regex sees the key before it is trimmed.
			group = jsTrim(extractUploadGroup(groupPattern, f.key))
		}

		if grouping && group != "" {
			if out.GroupKeys == nil {
				out.GroupKeys = map[string]map[string]client.DataQualityFieldSetting{}
			}
			if out.GroupKeys[group] == nil {
				out.GroupKeys[group] = map[string]client.DataQualityFieldSetting{}
			}
			out.GroupKeys[group][fieldKey] = setting
			continue
		}
		if out.Keys == nil {
			out.Keys = map[string]client.DataQualityFieldSetting{}
		}
		out.Keys[fieldKey] = setting
	}

	for _, key := range configOrder {
		if !fieldKeys[key] {
			warn(key, "is listed in %s but is not a field of the sample event, so it is ignored", dataCheckTypesKey)
		}
	}

	return out, nil
}

// uploadFieldSetting builds the setting of one sample field from its $dataCheckTypes entry.
func uploadFieldSetting(f sampleField, config map[string]*jsonValue, fieldKeys map[string]bool,
	warn func(field, format string, args ...any)) client.DataQualityFieldSetting {

	inferred := dqFieldString
	if f.number {
		inferred = dqFieldNumber
	}

	entry, listed := config[f.key]
	if !listed {
		return client.DataQualityFieldSetting{
			FieldType:        stringValue(inferred),
			DataQualityTypes: []string{},
			IsPrimaryKey:     boolValue(false),
		}
	}
	if entry.kind != jsonArray {
		warn(f.key, "its %s entry is not an array, so the field gets the Workbench's default checks", dataCheckTypesKey)
		return client.DataQualityFieldSetting{
			FieldType:        stringValue(inferred),
			DataQualityTypes: uploadDefaultChecks(inferred),
			IsPrimaryKey:     boolValue(false),
		}
	}

	r := resolveUploadChecks(entry.items, inferred, f.key, fieldKeys, warn)

	setting := client.DataQualityFieldSetting{
		FieldType:        stringValue(r.fieldType),
		DataQualityTypes: r.checks,
		IsPrimaryKey:     boolValue(r.isPrimaryKey),
		Threshold:        r.threshold,
		CustomName:       r.customName,
		CustomRegex:      r.customRegex,
		ConsistencyKeys:  r.consistencyKeys,
	}
	if setting.DataQualityTypes == nil {
		setting.DataQualityTypes = []string{}
	}
	if len(r.regexConfig) > 0 {
		setting.RegexConfig = r.regexConfig
	}
	if len(r.descriptions) > 0 {
		setting.DataQualityDescriptions = r.descriptions
	}
	return setting
}

type resolvedUploadChecks struct {
	fieldType       string
	checks          []string
	isPrimaryKey    bool
	threshold       *string
	customName      *string
	customRegex     *string
	consistencyKeys []client.ConsistencyKey
	regexConfig     map[string]client.DataQualityRegexSetting
	descriptions    map[string]string
}

// resolveUploadChecks is the UI's resolveFieldChecks.
func resolveUploadChecks(items []jsonValue, inferred, fieldKey string, fieldKeys map[string]bool,
	warn func(field, format string, args ...any)) resolvedUploadChecks {

	w := func(format string, args ...any) { warn(fieldKey, format, args...) }

	declared := ""
	var afterType []jsonValue
	for _, item := range items {
		if item.kind == jsonString {
			kw, param, hasParam := splitKeyword(item.str)
			if kw == "type" || kw == "fieldtype" {
				val := ""
				if hasParam {
					val = jsLower(jsTrim(param))
				}
				ft, ok := uploadFieldTypeKeywords[val]
				if !ok {
					w("unrecognised type declaration %q is ignored", val)
					continue
				}
				if declared != "" && declared != ft {
					w("declares more than one type; the last one wins")
				}
				declared = ft
				continue
			}
		}
		afterType = append(afterType, item)
	}

	r := resolvedUploadChecks{}
	var checkItems []jsonValue
	for _, item := range afterType {
		if item.kind == jsonString && jsLower(jsTrim(item.str)) == "primarykey" {
			r.isPrimaryKey = true
			continue
		}
		checkItems = append(checkItems, item)
	}

	r.fieldType = inferred
	if declared != "" {
		r.fieldType = declared
	}
	allowed := uploadAllowedChecks[r.fieldType]

	if len(checkItems) == 0 {
		if len(items) > 0 && (declared != "" || r.isPrimaryKey) {
			r.checks = uploadDefaultChecks(r.fieldType)
		}
		return r
	}

	selected := map[string]bool{}
	selectCheck := func(check string) {
		if !selected[check] {
			selected[check] = true
			r.checks = append(r.checks, check)
		}
	}
	isAllowed := func(check, label string) bool {
		if !allowed[check] {
			w("%q does not apply to a %s field and is ignored", label, r.fieldType)
			return false
		}
		return true
	}
	customCount := 0
	setThreshold := func(th string) {
		r.threshold = stringValue(th)
		if !uploadThresholdPattern.MatchString(th) {
			w("range threshold %q is missing or invalid; it is sent as written", th)
		}
	}
	setCustom := func(name, regex string) {
		customCount++
		if customCount > 1 {
			w("has more than one custom check; only the last one is kept")
		}
		r.customName = stringValue(name)
		r.customRegex = stringValue(regex)
		if name == "" || regex == "" {
			w("custom check is missing its name or regex; it is sent as written")
		}
	}
	addConsistency := func(key, value string) {
		r.consistencyKeys = append(r.consistencyKeys, client.ConsistencyKey{Key: key, Value: stringValue(value)})
		if !fieldKeys[key] {
			w("consistency key %q is not a field of the sample event", key)
		}
	}
	pairs := func(v *jsonValue, which string) []string {
		var out []string
		if v == nil || v.kind != jsonObject {
			return out
		}
		for _, m := range v.members {
			if m.key == "" || m.value.kind == jsonNull || jsString(m.value) == "" {
				w("%s has an empty key or value, which is dropped", which)
				continue
			}
			if !fieldKeys[m.key] {
				w("%s key %q is not a field of the sample event", which, m.key)
			}
			out = append(out, m.key+"="+jsString(m.value))
		}
		return out
	}
	applyWhenFill := func(check string, cfg jsonValue) {
		when, _ := cfg.member("when")
		fill, hasFill := cfg.member("fill")
		cond := pairs(when, check+" when")
		var fillPairs []string
		if hasFill {
			fillPairs = pairs(fill, check+" fill")
			if len(cond) == 0 {
				w("%s has fill but no when, so fill is ignored", check)
			}
		}
		entry := client.DataQualityRegexSetting{}
		if len(cond) > 0 {
			entry.ConditionRegex = cond
			if len(fillPairs) > 0 {
				entry.DataFillingRegex = fillPairs
			}
		}
		if entry.ConditionRegex != nil || entry.DataFillingRegex != nil {
			if r.regexConfig == nil {
				r.regexConfig = map[string]client.DataQualityRegexSetting{}
			}
			r.regexConfig[check] = entry
		}
	}

	for _, item := range checkItems {
		switch item.kind {
		case jsonObject:
			if len(item.members) != 1 {
				w("a check object must have exactly one key; this one is ignored")
				continue
			}
			label := item.members[0].key
			check, ok := uploadCheckKeywords[jsLower(jsTrim(label))]
			if !ok {
				w("unrecognised check %q is ignored", label)
				continue
			}
			if !isAllowed(check, label) {
				continue
			}
			cfg := item.members[0].value
			if cfg.kind != jsonObject {
				cfg = jsonValue{kind: jsonObject}
			}

			desc := ""
			if d, ok := cfg.member("desc"); ok && d.kind == jsonString {
				desc = jsTrim(d.str)
			} else if d, ok := cfg.member("description"); ok && d.kind == jsonString {
				desc = jsTrim(d.str)
			}
			if desc != "" {
				if r.descriptions == nil {
					r.descriptions = map[string]string{}
				}
				r.descriptions[check] = desc
			}

			switch check {
			case dqRange:
				selectCheck(check)
				th := ""
				if v, ok := cfg.member("threshold"); ok && v.kind == jsonString {
					th = jsTrim(v.str)
				}
				setThreshold(th)
			case dqCustom:
				selectCheck(check)
				name, regex := "", ""
				if v, ok := cfg.member("name"); ok && v.kind == jsonString {
					name = v.str
				}
				if v, ok := cfg.member("regex"); ok && v.kind == jsonString {
					regex = v.str
				}
				setCustom(name, regex)
			case dqConsistency:
				selectCheck(check)
				var rules []jsonMember
				if v, ok := cfg.member("rules"); ok {
					rules = v.entries()
				}
				added := 0
				for _, m := range rules {
					if m.key == "" || m.value.kind == jsonNull || jsString(m.value) == "" {
						continue
					}
					addConsistency(m.key, jsString(m.value))
					added++
				}
				if added == 0 {
					w("consistency needs at least one key=value rule")
				}
			default:
				selectCheck(check)
			}
			applyWhenFill(check, cfg)

		case jsonString:
			kw, param, _ := splitKeyword(item.str)
			check, ok := uploadCheckKeywords[kw]
			if !ok {
				w("unrecognised check %q is ignored", item.str)
				continue
			}
			if !isAllowed(check, kw) {
				continue
			}
			switch check {
			case dqMissing, dqOutliers, dqTypeMismatch, dqAnomalous:
				if jsTrim(param) != "" {
					w("%q takes no parameter; the parameter is ignored", kw)
				}
				selectCheck(check)
			case dqRange:
				selectCheck(check)
				setThreshold(jsTrim(param))
			case dqConsistency:
				selectCheck(check)
				eq := strings.Index(param, "=")
				if eq == -1 {
					w("consistency needs the form key=value: %q", item.str)
					continue
				}
				k, v := jsTrim(param[:eq]), param[eq+1:]
				if k == "" || v == "" {
					w("consistency has an empty key or value: %q", item.str)
					continue
				}
				addConsistency(k, v)
			case dqCustom:
				name, regex := "", ""
				if eq := strings.Index(param, "="); eq != -1 {
					name, regex = jsTrim(param[:eq]), param[eq+1:]
				}
				selectCheck(check)
				setCustom(name, regex)
			}

		default:
			w("unrecognised check is ignored")
		}
	}

	return r
}

// splitKeyword splits a check string at its first colon. The keyword is trimmed and lower-cased;
// the parameter is kept exactly as written, since a regex may contain anything.
func splitKeyword(s string) (keyword, param string, hasParam bool) {
	before, after, found := strings.Cut(s, ":")
	return jsLower(jsTrim(before)), after, found
}

// extractUploadGroup is the UI's extractGroup: capture group 1, or the whole match when group 1 did
// not take part.
func extractUploadGroup(pattern *regexp.Regexp, key string) string {
	loc := pattern.FindStringSubmatchIndex(key)
	if loc == nil {
		return ""
	}
	if len(loc) >= 4 && loc[2] >= 0 {
		return key[loc[2]:loc[3]]
	}
	return key[loc[0]:loc[1]]
}

type sampleField struct {
	key    string
	number bool
}

// flattenSampleEvent is the UI's transformDataToTargetFormat: nested keys are joined with "->",
// array elements by index, and an empty object or array yields no field.
func flattenSampleEvent(doc jsonValue) []sampleField {
	var out []sampleField
	var walk func(key string, v jsonValue)
	walk = func(key string, v jsonValue) {
		switch v.kind {
		case jsonObject, jsonArray:
			for _, m := range v.entries() {
				walk(key+"->"+m.key, m.value)
			}
		default:
			out = append(out, sampleField{key: key, number: v.kind == jsonNumber})
		}
	}
	for _, m := range doc.members {
		if m.key == dataCheckTypesKey {
			continue
		}
		walk(m.key, m.value)
	}
	return out
}

// An ordered JSON document. Go maps lose key order, and the order of the sample event is the order
// of the field table, which the schema check compares records against.

type jsonKind int

const (
	jsonNull jsonKind = iota
	jsonBool
	jsonNumber
	jsonString
	jsonArray
	jsonObject
)

type jsonMember struct {
	key   string
	value jsonValue
}

type jsonValue struct {
	kind    jsonKind
	boolean bool
	number  json.Number
	str     string
	items   []jsonValue
	members []jsonMember
}

func (v *jsonValue) member(key string) (*jsonValue, bool) {
	if v.kind != jsonObject {
		return nil, false
	}
	for i := range v.members {
		if v.members[i].key == key {
			return &v.members[i].value, true
		}
	}
	return nil, false
}

// entries lists an object's members, or an array's elements keyed by index, as JavaScript's
// Object.entries does.
func (v *jsonValue) entries() []jsonMember {
	switch v.kind {
	case jsonObject:
		return v.members
	case jsonArray:
		out := make([]jsonMember, len(v.items))
		for i, item := range v.items {
			out[i] = jsonMember{key: strconv.Itoa(i), value: item}
		}
		return out
	}
	return nil
}

func parseOrderedJSON(raw string) (jsonValue, error) {
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	v, err := decodeOrderedValue(dec)
	if err != nil {
		return jsonValue{}, fmt.Errorf("the upload file is not valid JSON: %w", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return jsonValue{}, errors.New("the upload file is not valid JSON: unexpected content after the top-level value")
	}
	return v, nil
}

func decodeOrderedValue(dec *json.Decoder) (jsonValue, error) {
	tok, err := dec.Token()
	if err != nil {
		return jsonValue{}, err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			obj := jsonValue{kind: jsonObject}
			index := map[string]int{}
			for dec.More() {
				keyTok, err := dec.Token()
				if err != nil {
					return jsonValue{}, err
				}
				key, ok := keyTok.(string)
				if !ok {
					return jsonValue{}, fmt.Errorf("unexpected object key %v", keyTok)
				}
				val, err := decodeOrderedValue(dec)
				if err != nil {
					return jsonValue{}, err
				}
				// As in JSON.parse, a repeated key keeps its first position and its last value.
				if i, dup := index[key]; dup {
					obj.members[i].value = val
					continue
				}
				index[key] = len(obj.members)
				obj.members = append(obj.members, jsonMember{key: key, value: val})
			}
			if _, err := dec.Token(); err != nil {
				return jsonValue{}, err
			}
			sortLikeJavaScript(obj.members)
			return obj, nil
		case '[':
			arr := jsonValue{kind: jsonArray}
			for dec.More() {
				val, err := decodeOrderedValue(dec)
				if err != nil {
					return jsonValue{}, err
				}
				arr.items = append(arr.items, val)
			}
			if _, err := dec.Token(); err != nil {
				return jsonValue{}, err
			}
			return arr, nil
		}
		return jsonValue{}, fmt.Errorf("unexpected delimiter %v", t)
	case string:
		return jsonValue{kind: jsonString, str: t}, nil
	case json.Number:
		return jsonValue{kind: jsonNumber, number: t}, nil
	case bool:
		return jsonValue{kind: jsonBool, boolean: t}, nil
	case nil:
		return jsonValue{kind: jsonNull}, nil
	}
	return jsonValue{}, fmt.Errorf("unexpected token %v", tok)
}

// sortLikeJavaScript applies JavaScript's property order: keys that are array indices come first
// in ascending numeric order, then every other key in insertion order.
func sortLikeJavaScript(members []jsonMember) {
	sort.SliceStable(members, func(i, j int) bool {
		a, aIndex := arrayIndexKey(members[i].key)
		b, bIndex := arrayIndexKey(members[j].key)
		switch {
		case aIndex && bIndex:
			return a < b
		case aIndex:
			return true
		default:
			return false
		}
	})
}

func arrayIndexKey(key string) (uint64, bool) {
	if key == "" || (len(key) > 1 && key[0] == '0') {
		return 0, false
	}
	for _, c := range key {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseUint(key, 10, 64)
	if err != nil || n >= math.MaxUint32 {
		return 0, false
	}
	return n, true
}

// jsString renders a value the way JavaScript's String() does, which is how the UI turns when,
// fill and consistency values into "key=value" strings.
func jsString(v jsonValue) string {
	switch v.kind {
	case jsonString:
		return v.str
	case jsonBool:
		if v.boolean {
			return "true"
		}
		return "false"
	case jsonNull:
		return "null"
	case jsonNumber:
		f, err := v.number.Float64()
		if err != nil {
			return v.number.String()
		}
		return jsNumberString(f)
	case jsonArray:
		parts := make([]string, len(v.items))
		for i, item := range v.items {
			if item.kind != jsonNull {
				parts[i] = jsString(item)
			}
		}
		return strings.Join(parts, ",")
	case jsonObject:
		return "[object Object]"
	}
	return ""
}

func jsNumberString(f float64) string {
	if f == 0 {
		return "0"
	}
	abs := math.Abs(f)
	if abs >= 1e21 || abs < 1e-6 {
		s := strconv.FormatFloat(f, 'e', -1, 64)
		mantissa, exp, _ := strings.Cut(s, "e")
		if !strings.HasPrefix(exp, "-") {
			exp = "+" + strings.TrimPrefix(exp, "+")
		}
		exp = strings.Replace(strings.Replace(exp, "+0", "+", 1), "-0", "-", 1)
		return mantissa + "e" + exp
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// jsTrim is JavaScript's String.prototype.trim: its whitespace includes U+FEFF and excludes U+0085,
// where Go's strings.TrimSpace does the opposite.
func jsTrim(s string) string {
	return strings.TrimFunc(s, func(r rune) bool {
		switch r {
		case '\t', '\n', '\v', '\f', '\r', ' ', '\u00a0', '\u1680', '\u2028', '\u2029', '\u202f', '\u205f', '\u3000', '\ufeff':
			return true
		}
		return r >= '\u2000' && r <= '\u200a'
	})
}

// jsLower is JavaScript's String.prototype.toLowerCase as far as matching ASCII keywords goes. The
// one case where Go's simple mapping differs to the keywords' benefit is U+0130, which JavaScript
// lower-cases to "i" plus a combining dot, so "İnvalidValue" is not a keyword in the Workbench.
func jsLower(s string) string {
	return strings.ToLower(strings.ReplaceAll(s, "\u0130", "i\u0307"))
}

func stringValue(s string) *string { return &s }
func boolValue(b bool) *bool       { return &b }
func int64Value(i int64) *int64    { return &i }

// validateUploadJSON reports what makes a file unusable at all, for config validation; everything
// else surfaces as a plan warning.
func validateUploadJSON(raw string) error {
	doc, err := parseOrderedJSON(raw)
	if err != nil {
		return err
	}
	if doc.kind != jsonObject {
		return errors.New("the upload file must be a JSON object: a sample event, optionally with a top-level \"$dataCheckTypes\" object")
	}
	return nil
}
