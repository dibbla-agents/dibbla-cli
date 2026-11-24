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
