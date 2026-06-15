package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
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

// AIDEV-NOTE: the CLI flag values live in the package-global `cli` (cobra binds flags to it).
// newTestRoot resets it per run so sequential in-test invocations don't inherit a previous
// --json/--api. Production runs the process once, so the global is fine there.
func newTestRoot() *cobra.Command {
	cli = cliFlags{} // reset global flag state between runs
	return newRoot()
}
