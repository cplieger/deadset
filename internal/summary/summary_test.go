package summary_test

import (
	"encoding/json"
	"path"
	"strings"
	"testing"

	spec "github.com/cplieger/deadset-spec/v8"
	"github.com/cplieger/deadset/v2/internal/report"
	"github.com/cplieger/deadset/v2/internal/summary"
)

// vector decodes one merged report of the published merge vectors, named by
// its case.
func vector(t *testing.T, name string) *report.Report {
	t.Helper()

	file := path.Join("vectors/merge", name, "expected.json")
	data, err := spec.Vectors.ReadFile(file)
	if err != nil {
		t.Fatalf("Setup: read %s: %v", file, err)
	}
	r, err := report.Decode(data)
	if err != nil {
		t.Fatalf("Setup: decode %s: %v", file, err)
	}
	return r
}

// annotations is every line Annotations writes for r.
func annotations(t *testing.T, r *report.Report) []string {
	t.Helper()

	var written strings.Builder
	if err := summary.Annotations(&written, r); err != nil {
		t.Fatalf("Annotations = error %v", err)
	}
	return strings.SplitAfter(written.String(), "\n")[:strings.Count(written.String(), "\n")]
}

// annotation is the one line Annotations writes for a report holding one
// record.
func annotation(t *testing.T, r *report.Report) string {
	t.Helper()

	lines := annotations(t, r)
	if len(lines) != 1 {
		t.Fatalf("Annotations(a report holding one record) = %q, want one line", lines)
	}
	return lines[0]
}

// TestWriteNamesEveryAnalyzerAndEveryCountOverEveryLanguage pins the analyzer
// lines and the summary line over published merged reports: two languages, one
// language from two analyzers, and the singular of each count.
func TestWriteNamesEveryAnalyzerAndEveryCountOverEveryLanguage(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		want string
	}{
		{
			name: "two-reports-no-edges",
			want: "analyzer deadset-go 1.0.0 sha256:ca44c6f01c073df92ee422025a7309547f821efd8d944692469f0aa8f2e92810\n" +
				"analyzer deadset-ts 1.0.0 sha256:5c5ab9f4a00f39a12023fa05a42d8015800d96cca15e10cd6c920a313ee0745d\n" +
				"summary: 4 findings (0 allow, 0 warn, 4 deny), 23 deletable lines, 3 suppressions in effect, " +
				"4 reasons recorded, 0 stale suppressions, 0 pending, 0 omitted, across go and ts\n",
		},
		{
			name: "two-analyzers-one-language",
			want: "analyzer deadset-go 1.1.0 sha256:10db00259693b895ebea73fdd977e2e93ff5adf6edebf0d14b2c167699cab2bd\n" +
				"analyzer example-go 1.0.0 sha256:967b1405d2841cfca2f226634b232a14f13b18616d14994da58485b9f0c2b950\n" +
				"summary: 4 findings (0 allow, 0 warn, 4 deny), 31 deletable lines, 0 suppressions in effect, " +
				"0 reasons recorded, 0 stale suppressions, 0 pending, 0 omitted, across go\n",
		},
		{
			name: "one-report",
			want: "analyzer deadset-go 1.0.0 sha256:ca44c6f01c073df92ee422025a7309547f821efd8d944692469f0aa8f2e92810\n" +
				"summary: 1 finding (0 allow, 0 warn, 1 deny), 8 deletable lines, 0 suppressions in effect, " +
				"0 reasons recorded, 0 stale suppressions, 0 pending, 0 omitted, across go\n",
		},
		{
			name: "stale-suppression-carried",
			want: "analyzer deadset-go 1.0.0 sha256:ca44c6f01c073df92ee422025a7309547f821efd8d944692469f0aa8f2e92810\n" +
				"summary: 0 findings (0 allow, 0 warn, 0 deny), 0 deletable lines, 0 suppressions in effect, " +
				"1 reason recorded, 1 stale suppression, 0 pending, 0 omitted, across go\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var written strings.Builder
			if err := summary.Write(&written, vector(t, tc.name)); err != nil {
				t.Fatalf("Write(%s) = error %v", tc.name, err)
			}
			if got := written.String(); got != tc.want {
				t.Errorf("Write(%s) =\n%s\nwant\n%s", tc.name, got, tc.want)
			}
		})
	}
}

// TestWriteNamesEachCountInItsOwnColumn pins each count of the summary line to
// its column, over totals that hold a different value in every column.
func TestWriteNamesEachCountInItsOwnColumn(t *testing.T) {
	t.Parallel()

	r := vector(t, "one-report")
	r.MergedFrom = nil
	r.Totals.BySeverity = report.BySeverity{Allow: 2, Warn: 3, Deny: 4}
	r.Totals.Pending = 5
	r.Totals.Omitted = 6

	var written strings.Builder
	if err := summary.Write(&written, r); err != nil {
		t.Fatalf("Write(one-report with distinct counts) = error %v", err)
	}
	want := "summary: 1 finding (2 allow, 3 warn, 4 deny), 8 deletable lines, 0 suppressions in effect, " +
		"0 reasons recorded, 0 stale suppressions, 5 pending, 6 omitted, across go\n"
	if got := written.String(); got != want {
		t.Errorf("Write(one-report with distinct counts) =\n%s\nwant\n%s", got, want)
	}
}

// TestAnnotationsWriteOneLinePerRecord pins one error annotation per deny
// finding and per stale suppression, and nothing else, over published merged
// reports.
func TestAnnotationsWriteOneLinePerRecord(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		want []string
	}{
		{
			name: "two-reports-no-edges",
			want: []string{
				"::error file=internal/store/store.go,line=19,col=6,endLine=27,title=DS1001 unused-exported::" +
					"exported function has no reference in the target and none from any loaded consumer\n",
				"::error file=internal/store/store.go,line=41,col=6,endLine=48,title=DS1002 unused-unexported::" +
					"unexported function has no reference in the target\n",
				"::error file=web/src/tabs.ts,line=31,col=3,endLine=31,title=DS1003 unused-member::" +
					"private class member has no reference in the target\n",
				"::error file=web/src/wire.ts,line=8,col=18,endLine=12,title=DS1001 unused-exported::" +
					"exported interface has no reference in the target and none from any loaded consumer\n",
			},
		},
		{
			name: "stale-suppression-carried",
			want: []string{
				"::error file=deadset-ignore.json,line=4,col=5,endLine=4,title=DS1703 stale-suppression::" +
					"ignore entry for DS1001 matches no current finding\n",
			},
		},
		{name: "pending-pair-live", want: []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := annotations(t, vector(t, tc.name))
			if strings.Join(got, "") != strings.Join(tc.want, "") {
				t.Errorf("Annotations(%s) =\n%s\nwant\n%s", tc.name, strings.Join(got, ""), strings.Join(tc.want, ""))
			}
		})
	}
}

// TestAnnotationsCommandFollowsTheSeverityAlone pins that a finding is an error
// for deny, a warning for warn and a notice for allow, and a stale suppression
// an error.
func TestAnnotationsCommandFollowsTheSeverityAlone(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		severity report.Severity
		want     string
	}{
		{severity: report.SeverityDeny, want: "::error "},
		{severity: report.SeverityWarn, want: "::warning "},
		{severity: report.SeverityAllow, want: "::notice "},
	} {
		t.Run(string(tc.severity), func(t *testing.T) {
			t.Parallel()

			r := vector(t, "one-report")
			r.Findings[0].Severity = tc.severity
			if got := annotation(t, r); !strings.HasPrefix(got, tc.want) {
				t.Errorf("Annotations(one-report with a %s finding) = %q, want a line starting %q", tc.severity, got, tc.want)
			}
		})
	}

	if got := annotation(t, vector(t, "stale-suppression-carried")); !strings.HasPrefix(got, "::error ") {
		t.Errorf("Annotations(stale-suppression-carried) = %q, want a line starting %q", got, "::error ")
	}
}

// TestAnnotationsWarnOfEachTypeErrorSkipAndNote pins one warning per type-error
// skip at its file and line and one per note at its directory, after the
// findings.
func TestAnnotationsWarnOfEachTypeErrorSkipAndNote(t *testing.T) {
	t.Parallel()

	r := vector(t, "one-report")
	r.TypeErrorSkips = []report.TypeErrorSkip{{Path: "app/main.go", Line: 9, Message: "undefined: missing"}}
	r.Notes = []report.Note{{Kind: report.NoteModuleNotLoaded, Path: "tools", Message: "the module, at tools, did not load"}}

	got := annotations(t, r)
	want := []string{
		"::warning file=app/main.go,line=9,title=type error skipped::" +
			"the analysis did not evaluate the function or statement holding this type error: undefined: missing\n",
		"::warning file=tools,title=" + string(report.NoteModuleNotLoaded) + "::the module, at tools, did not load\n",
	}
	if len(got) != 3 || strings.Join(got[1:], "") != strings.Join(want, "") {
		t.Errorf("Annotations(one-report with a skip and a note) =\n%s\nwant the finding, then\n%s", strings.Join(got, ""), strings.Join(want, ""))
	}
}

// TestAnnotationsEscapeWhatWouldEndAPropertyOrTheLine pins the escapes of the
// workflow command grammar, each in the value it would truncate.
func TestAnnotationsEscapeWhatWouldEndAPropertyOrTheLine(t *testing.T) {
	t.Parallel()

	r := vector(t, "one-report")
	r.Findings[0].Position.Path = "odd:name,dir/100%.go"
	r.Findings[0].Kind = "kind:with,separators"
	r.Findings[0].Message = "100% unreferenced, line\r\nbreak"

	want := "::error file=odd%3Aname%2Cdir/100%25.go,line=41,col=6,endLine=48," +
		"title=DS1002 kind%3Awith%2Cseparators::100%25 unreferenced, line%0D%0Abreak\n"
	if got := annotation(t, r); got != want {
		t.Errorf("Annotations(a finding whose values hold separators) = %q, want %q", got, want)
	}
}

// TestTheStaleSuppressionTitleIsTheContractsKindName pins the kind a stale
// suppression's annotation is titled with to the name contract/kinds.json gives
// its code.
func TestTheStaleSuppressionTitleIsTheContractsKindName(t *testing.T) {
	t.Parallel()

	body, err := spec.Contract.ReadFile("contract/kinds.json")
	if err != nil {
		t.Fatalf("Setup: read contract/kinds.json: %v", err)
	}
	var vocabulary struct {
		Kinds []struct {
			Code string `json:"code"`
			Name string `json:"name"`
		} `json:"kinds"`
	}
	if err := json.Unmarshal(body, &vocabulary); err != nil {
		t.Fatalf("Setup: decode contract/kinds.json: %v", err)
	}
	stale := vector(t, "stale-suppression-carried")
	code := stale.StaleSuppressions[0].Code
	name := ""
	for _, kind := range vocabulary.Kinds {
		if kind.Code == code {
			name = kind.Name
		}
	}
	if name == "" {
		t.Fatalf("Setup: contract/kinds.json holds no live row for %s", code)
	}

	if got, want := annotation(t, stale), ",title="+code+" "+name+"::"; !strings.Contains(got, want) {
		t.Errorf("Annotations(stale-suppression-carried) = %q, want it to carry %q", got, want)
	}
}
