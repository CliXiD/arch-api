package model

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// callsSchema is the seeded schema of the `calls` edge type.
const callsSchema = `{"type":"object","properties":{
	"protocol":{"type":"string","enum":["http","https","grpc","tcp","amqp","kafka","other"]},
	"port":{"type":"number","minimum":1,"maximum":65535}}}`

func decode(t *testing.T, s string) map[string]any {
	t.Helper()
	m := map[string]any{}
	require.NoError(t, json.Unmarshal([]byte(s), &m))
	return m
}

func TestValidateAttributes(t *testing.T) {
	open := `{"type":"object","properties":{}}`
	strict := `{"type":"object","required":["tier"],"additionalProperties":false,"properties":{
		"tier":{"type":"string"},"zones":{"type":"array","items":{"type":"string"}},"public":{"type":"boolean"}}}`
	tests := []struct {
		name   string
		schema string
		attrs  string
		want   ValidationErrors
	}{
		{"seeded calls edge", callsSchema, `{"protocol":"https","port":443}`, nil},
		{"no attributes", callsSchema, `{}`, nil},
		{"undeclared supported values are allowed", open, `{"team_page":"x","replicas":3,"public":true,"zones":["a","b"]}`, nil},
		{"object value", open, `{"settings":{"a":1}}`,
			ValidationErrors{{Location: "body.attributes.settings", Message: "unsupported attribute type"}}},
		{"null value", open, `{"note":null}`,
			ValidationErrors{{Location: "body.attributes.note", Message: "unsupported attribute type"}}},
		{"mixed array", open, `{"zones":["a",1]}`,
			ValidationErrors{{Location: "body.attributes.zones", Message: "unsupported attribute type"}}},
		{"port over maximum", callsSchema, `{"port":70000}`,
			ValidationErrors{{Location: "body.attributes.port", Message: "must be at most 65535"}}},
		{"port under minimum", callsSchema, `{"port":0}`,
			ValidationErrors{{Location: "body.attributes.port", Message: "must be at least 1"}}},
		{"port as a string", callsSchema, `{"port":"443"}`,
			ValidationErrors{{Location: "body.attributes.port", Message: "must be of type number"}}},
		{"protocol not in enum", callsSchema, `{"protocol":"smtp"}`,
			ValidationErrors{{Location: "body.attributes.protocol", Message: "is not one of the allowed values"}}},
		{"every failure, ordered by location", callsSchema, `{"protocol":"smtp","port":70000,"extra":{}}`,
			ValidationErrors{
				{Location: "body.attributes.extra", Message: "unsupported attribute type"},
				{Location: "body.attributes.port", Message: "must be at most 65535"},
				{Location: "body.attributes.protocol", Message: "is not one of the allowed values"},
			}},
		{"strict schema valid", strict, `{"tier":"gold","zones":["a"],"public":false}`, nil},
		{"required missing and undeclared rejected", strict, `{"zones":["a"],"color":"red"}`,
			ValidationErrors{
				{Location: "body.attributes.color", Message: "is not allowed by the type's schema"},
				{Location: "body.attributes.tier", Message: "is required"},
			}},
		{"boolean of the wrong type", strict, `{"tier":"gold","public":"yes"}`,
			ValidationErrors{{Location: "body.attributes.public", Message: "must be of type boolean"}}},
		{"array of the wrong type", strict, `{"tier":"gold","zones":"a"}`,
			ValidationErrors{{Location: "body.attributes.zones", Message: "must be of type array"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := validateAttributes(decode(t, tt.schema), decode(t, tt.attrs))
			assert.Equal(t, tt.want, got)
			for _, e := range got {
				assert.NotContains(t, e.Message, "70000", "messages never hold the value")
				assert.NotContains(t, e.Message, "smtp", "messages never hold the value")
			}
		})
	}
}

func TestCheckKindPair(t *testing.T) {
	runsOn := []KindPair{{From: "application", To: "infrastructure"}}
	calls := []KindPair{{From: "application", To: "application"}, {From: "application", To: "external"}}
	tests := []struct {
		name     string
		edgeType string
		allowed  []KindPair
		from, to string
		want     *ValidationError
	}{
		{"allowed pair", "runs_on", runsOn, "application", "infrastructure", nil},
		{"from kind starts no allowed pair", "calls", calls, "infrastructure", "application",
			&ValidationError{Location: "body.from", Message: "edge type calls does not allow infrastructure → application"}},
		{"to kind not allowed from an allowed from kind", "calls", calls, "application", "infrastructure",
			&ValidationError{Location: "body.to", Message: "edge type calls does not allow application → infrastructure"}},
		{"runs_on to a network", "runs_on", runsOn, "application", "network",
			&ValidationError{Location: "body.to", Message: "edge type runs_on does not allow application → network"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, checkKindPair(tt.edgeType, tt.allowed, tt.from, tt.to))
		})
	}
}

func TestValidateSchema(t *testing.T) {
	const loc = "body.attributes_schema"
	prop := func(name string) string { return loc + ".properties." + name }
	one := func(location, msg string) ValidationErrors {
		return ValidationErrors{{Location: location, Message: msg}}
	}
	tests := []struct {
		name   string
		schema string
		want   ValidationErrors
	}{
		{"seeded default schema", `{"type":"object","properties":{}}`, nil},
		{"seeded calls schema", callsSchema, nil},
		{"every accepted keyword", `{"type":"object","required":["tier"],"additionalProperties":false,"properties":{
			"tier":{"type":"string","enum":["gold","silver"]},"name":{"type":"string"},
			"replicas":{"type":"number","enum":[1,3,5]},"port":{"type":"number","minimum":1,"maximum":65535},
			"floor":{"type":"number","minimum":0},"public":{"type":"boolean"},
			"zones":{"type":"array","items":{"type":"string"}}}}`, nil},
		{"additionalProperties true", `{"type":"object","properties":{},"additionalProperties":true}`, nil},
		{"equal minimum and maximum", `{"type":"object","properties":{"n":{"type":"number","minimum":2,"maximum":2}}}`, nil},
		{"empty required", `{"type":"object","properties":{},"required":[]}`, nil},

		{"empty schema", `{}`, one(loc, msgSchemaType)},
		{"type not object", `{"type":"array","properties":{}}`, one(loc, msgSchemaType)},
		{"unknown top-level keyword", `{"type":"object","properties":{},"description":"x"}`, one(loc, msgSchemaKeyword)},
		{"properties missing", `{"type":"object"}`, one(loc, msgSchemaProperties)},
		{"properties not an object", `{"type":"object","properties":[]}`, one(loc, msgSchemaProperties)},
		{"additionalProperties not a boolean", `{"type":"object","properties":{},"additionalProperties":{}}`, one(loc, msgSchemaAdditional)},
		{"empty property name", `{"type":"object","properties":{"":{"type":"string"}}}`, one(loc+".properties", msgSchemaEmptyName)},
		{"property not an object", `{"type":"object","properties":{"x":"string"}}`, one(prop("x"), msgPropertyObject)},
		{"object property", `{"type":"object","properties":{"x":{"type":"object"}}}`, one(prop("x"), msgPropertyType)},
		{"property without type", `{"type":"object","properties":{"x":{"enum":["a"]}}}`, one(prop("x"), msgPropertyType)},
		{"pattern keyword", `{"type":"object","properties":{"x":{"type":"string","pattern":"^a"}}}`, one(prop("x"), msgSchemaKeyword)},
		{"enum on a boolean", `{"type":"object","properties":{"x":{"type":"boolean","enum":[true]}}}`, one(prop("x"), msgSchemaKeyword)},
		{"minimum on a string", `{"type":"object","properties":{"x":{"type":"string","minimum":1}}}`, one(prop("x"), msgSchemaKeyword)},
		{"items on a string", `{"type":"object","properties":{"x":{"type":"string","items":{"type":"string"}}}}`, one(prop("x"), msgSchemaKeyword)},
		{"empty enum", `{"type":"object","properties":{"x":{"type":"string","enum":[]}}}`, one(prop("x"), msgPropertyEnum)},
		{"enum not an array", `{"type":"object","properties":{"x":{"type":"string","enum":"a"}}}`, one(prop("x"), msgPropertyEnum)},
		{"enum of the wrong type", `{"type":"object","properties":{"x":{"type":"number","enum":[1,"2"]}}}`, one(prop("x"), msgPropertyEnum)},
		{"minimum not a number", `{"type":"object","properties":{"x":{"type":"number","minimum":"1"}}}`, one(prop("x"), msgPropertyLimit)},
		{"minimum above maximum", `{"type":"object","properties":{"x":{"type":"number","minimum":5,"maximum":1}}}`, one(prop("x"), msgPropertyRange)},
		{"array without items", `{"type":"object","properties":{"x":{"type":"array"}}}`, one(prop("x"), msgPropertyItems)},
		{"array of numbers", `{"type":"object","properties":{"x":{"type":"array","items":{"type":"number"}}}}`, one(prop("x"), msgPropertyItems)},
		{"items with another keyword", `{"type":"object","properties":{"x":{"type":"array","items":{"type":"string","enum":["a"]}}}}`,
			one(prop("x"), msgPropertyItems)},
		{"required not an array", `{"type":"object","properties":{"x":{"type":"string"}},"required":"x"}`, one(loc+".required", msgSchemaRequiredType)},
		{"required holds a number", `{"type":"object","properties":{"x":{"type":"string"}},"required":[1]}`, one(loc+".required", msgSchemaRequiredType)},
		{"required names an undeclared property", `{"type":"object","properties":{"x":{"type":"string"}},"required":["y"]}`,
			one(loc+".required", msgSchemaRequiredName)},
		{"required names a property twice", `{"type":"object","properties":{"x":{"type":"string"}},"required":["x","x"]}`,
			one(loc+".required", msgSchemaRequiredTwice)},
		{"every failure, ordered by location", `{"type":"object","required":["z"],"properties":{
			"b":{"type":"object"},"a":{"type":"string","format":"uri"}}}`,
			ValidationErrors{
				{Location: prop("a"), Message: msgSchemaKeyword},
				{Location: prop("b"), Message: msgPropertyType},
				{Location: loc + ".required", Message: msgSchemaRequiredName},
			}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := validateSchema(decode(t, tt.schema))
			assert.Equal(t, tt.want, got)
			for _, e := range got {
				assert.NotContains(t, e.Message, "uri", "messages never hold the value")
			}
		})
	}
}
