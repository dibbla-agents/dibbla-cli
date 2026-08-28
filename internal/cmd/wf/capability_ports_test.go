package wf

import (
	"encoding/json"
	"testing"
)

// The PORTS column renders the wireable "<seat>.<port>" names straight off a
// provider registration, so the payload here is the exact JSON shape the
// capability-providers endpoint returns (it serializes the whole definition).
func TestSummarizeExtraPorts(t *testing.T) {
	var provider map[string]interface{}
	raw := `{
		"capability": "memory",
		"name": "org-ports-memory",
		"extra_inputs_schema": {
			"properties": {"topic": {"type": "string"}, "decay": {"type": "number"}},
			"required": ["topic"]
		},
		"extra_outputs_schema": {
			"properties": {"facts_retrieved": {"type": "integer"}, "topic_echo": {"type": "string"}}
		}
	}`
	if err := json.Unmarshal([]byte(raw), &provider); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	got := summarizeExtraPorts("memory", provider["extra_inputs_schema"], provider["extra_outputs_schema"])
	want := "in memory.decay, memory.topic* · out memory.facts_retrieved, memory.topic_echo"
	if got != want {
		t.Errorf("summarizeExtraPorts() =\n  %q\nwant\n  %q", got, want)
	}
}

// A provider that declares no extra ports — every provider before DIB-449 —
// must leave the cell empty rather than printing punctuation for nothing.
func TestSummarizeExtraPortsAbsentOrMalformed(t *testing.T) {
	cases := []struct {
		name    string
		in, out interface{}
	}{
		{"both absent", nil, nil},
		{"empty objects", map[string]interface{}{}, map[string]interface{}{}},
		{"no properties", map[string]interface{}{"type": "object"}, nil},
		{"properties not an object", map[string]interface{}{"properties": "nope"}, nil},
		{"schema not an object", "nope", 42},
	}
	for _, tc := range cases {
		if got := summarizeExtraPorts("memory", tc.in, tc.out); got != "" {
			t.Errorf("%s: summarizeExtraPorts() = %q, want empty", tc.name, got)
		}
	}
}

// One-sided declarations are normal: a tool_search provider may only report
// outputs. Only the side that exists should appear.
func TestSummarizeExtraPortsOneSided(t *testing.T) {
	outOnly := map[string]interface{}{
		"properties": map[string]interface{}{"hits": map[string]interface{}{"type": "integer"}},
	}
	if got, want := summarizeExtraPorts("tool_search", nil, outOnly), "out tool_search.hits"; got != want {
		t.Errorf("outputs only = %q, want %q", got, want)
	}
	inOnly := map[string]interface{}{
		"properties": map[string]interface{}{"domain": map[string]interface{}{"type": "string"}},
		"required":   []interface{}{"domain"},
	}
	if got, want := summarizeExtraPorts("tool_search", inOnly, nil), "in tool_search.domain*"; got != want {
		t.Errorf("inputs only = %q, want %q", got, want)
	}
}
