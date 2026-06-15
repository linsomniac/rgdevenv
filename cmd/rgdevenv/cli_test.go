package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRenderJSON(t *testing.T) {
	var buf bytes.Buffer
	if err := renderJSON(&buf, map[string]any{"a": 1, "b": "x"}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, `"a": 1`) || !strings.Contains(out, `"b": "x"`) {
		t.Fatalf("json output = %q", out)
	}
}

func TestRenderTable(t *testing.T) {
	var buf bytes.Buffer
	renderTable(&buf, []string{"NAME", "PORT"}, [][]string{{"a", "443"}, {"bb", "8443"}})
	out := buf.String()
	if !strings.Contains(out, "NAME") || !strings.Contains(out, "8443") {
		t.Fatalf("table output = %q", out)
	}
	// header and both rows present
	if lines := strings.Count(strings.TrimRight(out, "\n"), "\n"); lines != 2 {
		t.Fatalf("want 3 lines (header+2 rows), got %d in %q", lines+1, out)
	}
}
