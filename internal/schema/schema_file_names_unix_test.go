//go:build unix

package schema

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/cplieger/deadset-spec/v8/report"
)

// schemaFileClasses is one schema file name per class of character a reference
// spells apart: a C0 control, a C1 control, a bidirectional control, the line
// separator, a space, % and #. A report path holds each as the file name does,
// and the report's path form refuses only a C0 control or DEL. A file system on
// unix admits every one.
var schemaFileClasses = []struct {
	label   string
	name    string
	refused bool
}{
	{"c0-control", "a\x1bb.proto", true},
	{"c1-control", "a\u009bb.proto", false},
	{"bidi-control", "safe\u202e.proto", false},
	{"line-separator", "a\u2028b.proto", false},
	{"space", "a b.proto", false},
	{"percent", "a%b.proto", false},
	{"number-sign", "a#b.proto", false},
}

// schemaFileFindings is the report's findings over the tree of
// [TestASchemaFileNameOfEveryClassIsReportedAsTheFileNamesIt] and its stale
// suppressions, as code and position, with FILE standing for the file whose name
// holds the class's character.
var schemaFileFindings = []string{"DS9001 FILE:7", "stale DS1801 FILE:4"}

// A schema file whose name holds any class of character the reference grammar
// spells apart is read, and the report names it as the file system does, with the
// same findings whatever the class; a report naming a C0 control breaks the
// report's path form.
func TestASchemaFileNameOfEveryClassIsReportedAsTheFileNamesIt(t *testing.T) {
	for _, class := range schemaFileClasses {
		t.Run(class.label, func(t *testing.T) {
			r, err := readTreeOrError(t, map[string]string{
				"plain.proto": "syntax = \"proto3\";\npackage v1;\n\nmessage Kept {}\n",
				class.name: "syntax = \"proto3\";\npackage v1;\n\n// deadset:ignore DS1801 -- bound to nothing\n\n" +
					"message Hidden {\n  int32 n = 1; // deadset:ignore DS1802\n}\n",
			}, Settings{})
			if err == nil {
				err = report.Validate(r.Report)
			}
			if class.refused {
				if err == nil {
					t.Fatal("the report naming the file is valid, want it refused by the path form")
				}
				return
			}
			if err != nil {
				t.Fatalf("Read = %v", err)
			}
			got := make([]string, len(r.Report.Findings))
			for i := range r.Report.Findings {
				f := &r.Report.Findings[i]
				got[i] = fmt.Sprintf("%s %s:%d", f.Code, f.Position.Path, f.Position.Line)
			}
			for i := range r.Report.StaleSuppressions {
				s := &r.Report.StaleSuppressions[i]
				got = append(got, fmt.Sprintf("stale %s %s:%d", s.Entry.Code, s.Position.Path, s.Position.Line))
			}
			want := make([]string, len(schemaFileFindings))
			for i, line := range schemaFileFindings {
				want[i] = strings.ReplaceAll(line, "FILE", class.name)
			}
			if !slices.Equal(got, want) {
				t.Errorf("the report holds\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
			}
		})
	}
}
