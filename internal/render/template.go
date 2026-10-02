package render

import (
	"bytes"
	"fmt"
	"io"
	"text/template"

	"github.com/cplieger/deadset/internal/report"
)

// templateName is the name an error gives the user's template.
const templateName = "report"

// Template is a user template, parsed, which renders a merged report.
type Template struct {
	parsed *template.Template
}

// ParseTemplate parses text as a text/template over a [report.Report], whose
// fields are the report's members under their Go names. A field the report
// does not carry fails the rendering rather than rendering as nothing.
func ParseTemplate(text string) (*Template, error) {
	parsed, err := template.New(templateName).Parse(text)
	if err != nil {
		return nil, fmt.Errorf("parse the template: %w", err)
	}
	return &Template{parsed: parsed}, nil
}

// Render writes r through the template. The rendering is built whole before
// anything is written, so a template that fails writes nothing.
func (t *Template) Render(w io.Writer, r *report.Report) error {
	var rendered bytes.Buffer
	if err := t.parsed.Execute(&rendered, r); err != nil {
		return fmt.Errorf("render the template: %w", err)
	}
	if _, err := w.Write(rendered.Bytes()); err != nil {
		return fmt.Errorf("render: write the template rendering: %w", err)
	}
	return nil
}
