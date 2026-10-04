package render

import (
	"bytes"
	"strings"
	"testing"
)

// testTemplate renders the members a template is most often written over, by
// their JSON member names: the analyzers merged, every finding and stale
// suppression in report order with the analyzer that carried it, a record the
// merge emitted naming the merge, and the totals.
const testTemplate = `{{range .merged_from}}analyzer {{.name}} {{.version}}
{{end}}{{range .findings}}{{$by := "merge"}}{{range $member, $value := .}}{{if eq $member "analyzer"}}{{$by = $value}}{{end}}{{end -}}
{{.position.path}}:{{.position.line}}:{{.position.column}} {{.code}} {{.kind}} {{.severity}} {{$by}}
{{end}}{{range .stale_suppressions}}{{.position.path}}:{{.position.line}}:{{.position.column}} {{.code}} {{.analyzer}}
{{end}}{{.totals.findings}} findings, {{.totals.stale_suppressions}} stale, {{.totals.omitted}} omitted
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

	if _, err := ParseTemplate("{{range .findings}}"); err == nil || !strings.Contains(err.Error(), "parse the template") {
		t.Errorf(`ParseTemplate("{{range .findings}}") = %v, want a parse refusal`, err)
	}
	parsed, err := ParseTemplate("before {{.totals.no_such_count}}")
	if err != nil {
		t.Fatalf("Setup: ParseTemplate = %v", err)
	}
	c := mergedCases(t)[0]
	var out bytes.Buffer
	if err := parsed.Render(&out, c.merged); err == nil || !strings.Contains(err.Error(), "no_such_count") || out.Len() != 0 {
		t.Errorf("Render(.totals.no_such_count) = %v and wrote %q, want a refusal naming the member and nothing written", err, out.String())
	}
}

// Every number form but a decimal integer, a block and a function outside the
// subset are refused at parse, naming the template's line.
func TestParseTemplateRefusesTheFormsOutsideTheSubset(t *testing.T) {
	t.Parallel()

	for name, text := range map[string]string{
		"leading-zero":  "{{print 01}}",
		"base-prefix":   "\n{{print 0x1f}}",
		"underscore":    "{{print 1_000}}",
		"exponent":      "{{print 1e2}}",
		"imaginary":     "{{print 1i}}",
		"block":         `{{block "x" .}}y{{end}}`,
		"define-itself": `{{define "report"}}y{{end}}`,
		"slice":         "{{slice .findings 0}}",
		"call":          "{{call .findings}}",
		"octal-in-else": `{{if .findings}}{{else}}{{print "\0"}}{{end}}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			parsed, err := ParseTemplate(text)
			if err == nil {
				t.Fatalf("ParseTemplate(%q) = %v, want a refusal", text, parsed)
			}
			if !strings.Contains(err.Error(), "report:") {
				t.Errorf("ParseTemplate(%q) = %v, want the refusal to name the template and its line", text, err)
			}
		})
	}
}

// A range over a value other than an array, an object or null fails the
// rendering, nil as the command of an action or as the pipeline of if, with or
// range fails it, and nil as a function's operand is the null value.
func TestTemplateRangeAndNil(t *testing.T) {
	t.Parallel()

	document := []byte(`{"count": 3, "name": "x", "none": null}`)
	for _, c := range []struct {
		text, want string
		fails      bool
	}{
		{text: "{{range .count}}x{{end}}", fails: true},
		{text: "{{range .name}}x{{end}}", fails: true},
		{text: "{{range $i, $v := .count}}x{{end}}", fails: true},
		{text: "{{range .none}}x{{else}}empty{{end}}", want: "empty"},
		{text: "{{.none}}|{{print nil}}|{{printf \"%v\" nil}}", want: "<no value>|<nil>|<nil>"},
		{text: "{{nil}}", fails: true},
		{text: "x{{nil | print}}", fails: true},
		{text: "{{if nil}}t{{else}}f{{end}}", fails: true},
		{text: "{{with nil}}t{{else}}f{{end}}", fails: true},
		{text: "{{range nil}}t{{else}}f{{end}}", fails: true},
	} {
		t.Run(c.text, func(t *testing.T) {
			t.Parallel()

			parsed, err := ParseTemplate(c.text)
			if err != nil {
				t.Fatalf("Setup: ParseTemplate(%q) = %v", c.text, err)
			}
			var out bytes.Buffer
			err = parsed.execute(&out, document)
			switch {
			case c.fails && (err == nil || out.Len() != 0):
				t.Errorf("render(%q) = %v and wrote %q, want a failure and nothing written", c.text, err, out.String())
			case !c.fails && (err != nil || out.String() != c.want):
				t.Errorf("render(%q) = %q, %v, want %q", c.text, out.String(), err, c.want)
			}
		})
	}
}

// eq and ne compare two booleans, and an ordering of two booleans fails.
func TestTemplateComparesBooleansForEqualityOnly(t *testing.T) {
	t.Parallel()

	parsed, err := ParseTemplate("{{eq true true}} {{eq .on false}} {{ne .on false}}")
	if err != nil {
		t.Fatalf("Setup: ParseTemplate = %v", err)
	}
	var out bytes.Buffer
	if err := parsed.execute(&out, []byte(`{"on": true}`)); err != nil || out.String() != "true false true" {
		t.Errorf("render(eq and ne of booleans) = %q, %v, want %q", out.String(), err, "true false true")
	}
	ordering, err := ParseTemplate("{{lt false true}}")
	if err != nil {
		t.Fatalf("Setup: ParseTemplate = %v", err)
	}
	if err := ordering.execute(&bytes.Buffer{}, []byte(`{}`)); err == nil {
		t.Errorf("render(lt of two booleans) = nil, want a failure")
	}
}
