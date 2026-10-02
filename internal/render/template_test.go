package render

import (
	"bytes"
	"strings"
	"testing"
)

// testTemplate renders the members a template is most often written over:
// the analyzers merged, every finding and stale suppression in report order,
// and the totals.
const testTemplate = `{{range .MergedFrom}}analyzer {{.Name}} {{.Version}}
{{end}}{{range .Findings}}{{.Position.Path}}:{{.Position.Line}}:{{.Position.Column}} {{.Code}} {{.Kind}} {{.Severity}} {{or .Analyzer "merge"}}
{{end}}{{range .StaleSuppressions}}{{.Position.Path}}:{{.Position.Line}}:{{.Position.Column}} {{.Code}} {{.Analyzer}}
{{end}}{{.Totals.Findings}} findings, {{.Totals.StaleSuppressions}} stale, {{.Totals.Omitted}} omitted
`

// The template rendering of every published merged report is its committed
// golden, byte for byte.
//
// UPDATE_GOLDEN=1 writes the goldens instead of comparing them.
func TestTemplateOfEveryMergedVector(t *testing.T) {
	t.Parallel()

	parsed, err := ParseTemplate(testTemplate)
	if err != nil {
		t.Fatalf("Setup: ParseTemplate = %v", err)
	}
	for _, c := range mergedCases(t) {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			var out bytes.Buffer
			if err := parsed.Render(&out, c.merged); err != nil {
				t.Fatalf("Render(%s) = %v", c.name, err)
			}
			compareGolden(t, "template", c.name+".txt", out.Bytes())
		})
	}
}

// A template that does not parse is refused at parse, and one naming a key
// the report does not carry fails its rendering and writes nothing.
func TestTemplateRefusals(t *testing.T) {
	t.Parallel()

	if _, err := ParseTemplate("{{range .Findings}}"); err == nil || !strings.Contains(err.Error(), "parse the template") {
		t.Errorf(`ParseTemplate("{{range .Findings}}") = %v, want a parse refusal`, err)
	}
	parsed, err := ParseTemplate("before {{.Totals.NoSuchCount}}")
	if err != nil {
		t.Fatalf("Setup: ParseTemplate = %v", err)
	}
	c := mergedCases(t)[0]
	var out bytes.Buffer
	if err := parsed.Render(&out, c.merged); err == nil || !strings.Contains(err.Error(), "NoSuchCount") || out.Len() != 0 {
		t.Errorf("Render(.Totals.NoSuchCount) = %v and wrote %q, want a refusal naming the field and nothing written", err, out.String())
	}
}
