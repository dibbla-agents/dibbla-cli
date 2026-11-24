package wf

import (
	"io"
	"os"
	"testing"
)

func TestPrintAPIFields_SortedAndMarksOptional(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout := os.Stdout
	os.Stdout = w
	printAPIFields(
		map[string]interface{}{"question": "string", "locale": "string"},
		stringSet([]interface{}{"locale"}),
	)
	os.Stdout = stdout
	w.Close()
	out, _ := io.ReadAll(r)

	want := "  locale                         string (optional)\n" +
		"  question                       string\n"
	if string(out) != want {
		t.Errorf("got:\n%q\nwant:\n%q", out, want)
	}
}

func TestStringSet_IgnoresNonStrings(t *testing.T) {
	if got := stringSet(nil); len(got) != 0 {
		t.Errorf("nil → %v", got)
	}
	if got := stringSet([]interface{}{"a", 1}); !got["a"] || len(got) != 1 {
		t.Errorf("got %v", got)
	}
}

// DIB-1907: wf validate names the inputs whose waiting is worth knowing and
// what a call without each optional field does; Required inputs that simply
// wait for their wire are counted.
func TestInputGatingLines(t *testing.T) {
	result := map[string]interface{}{
		"input_gating": []interface{}{
			map[string]interface{}{"node": "template", "input": "question", "mode": "required", "waits": "always",
				"message": "template.question: Required – waits for api_input.question"},
			map[string]interface{}{"node": "template", "input": "task", "mode": "required", "waits": "unless_omitted",
				"message": "template.task: Required – waits for api_input.task; runs without it when the call omits 'task'"},
		},
		"api_endpoints": []interface{}{
			map[string]interface{}{"node": "api_input", "omissions": []interface{}{
				map[string]interface{}{"field": "task", "effect": "A call without 'task' runs template.task without it."},
			}},
		},
	}
	got := inputGatingLines(result)
	want := []string{
		"Inputs:",
		"  template.task: Required – waits for api_input.task; runs without it when the call omits 'task'",
		"  1 other wired input is Required and waits for its wire on every call",
		"  api_input.task (optional): A call without 'task' runs template.task without it.",
	}
	if len(got) != len(want) {
		t.Fatalf("got %q", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d:\n got %q\nwant %q", i, got[i], want[i])
		}
	}
	if inputGatingLines(map[string]interface{}{}) != nil {
		t.Error("an answer without input_gating prints nothing")
	}
}

func TestOmissionLines(t *testing.T) {
	got := omissionLines([]interface{}{
		map[string]interface{}{"field": "task", "effect": "A call without 'task' runs template.task without it."},
	})
	want := "  task (optional)                A call without 'task' runs template.task without it."
	if len(got) != 1 || got[0] != want {
		t.Errorf("got %q", got)
	}
}
