package summary_test

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	spec "github.com/cplieger/deadset-spec/v7"
	"github.com/cplieger/deadset/internal/report"
	"github.com/cplieger/deadset/internal/summary"
)

// textLine is the expression contract/grammar/text-line.md defines the text
// line by, read from the page's expression block.
func textLine(t *testing.T) *regexp.Regexp {
	t.Helper()

	page, err := spec.Contract.ReadFile("contract/grammar/text-line.md")
	if err != nil {
		t.Fatalf("Setup: read text-line.md: %v", err)
	}
	_, section, found := strings.Cut(string(page), "## The expression")
	_, block, opened := strings.Cut(section, "```text\n")
	expression, _, closed := strings.Cut(block, "\n```")
	if !found || !opened || !closed {
		t.Fatal("Setup: text-line.md holds no expression block under its heading")
	}
	return regexp.MustCompile(expression)
}

// lines splits written text into its lines.
func lines(written string) []string {
	return strings.Split(strings.TrimSuffix(written, "\n"), "\n")
}

// Every finding and every stale suppression is one line the published
// expression accepts, in the report's order, its groups the record's fields.
func TestTextWritesOneLineThePageAcceptsPerRecord(t *testing.T) {
	t.Parallel()

	expression := textLine(t)
	type fields struct{ path, line, col, kind, name, message, confidence, code string }
	for _, name := range []string{"two-reports-no-edges", "stale-suppression-carried", "edge-absent-on-every-side"} {
		r := vector(t, name)
		var want []fields
		for i := range r.Findings {
			f := &r.Findings[i]
			want = append(want, fields{
				f.Position.Path, strconv.Itoa(f.Position.Line), strconv.Itoa(f.Position.Column),
				f.Symbol.Kind, f.Symbol.Name, f.Message, string(f.Confidence), f.Code,
			})
		}
		for i := range r.StaleSuppressions {
			s := &r.StaleSuppressions[i]
			want = append(want, fields{
				s.Position.Path, strconv.Itoa(s.Position.Line), strconv.Itoa(s.Position.Column),
				"suppression", s.Symbol, s.Message, string(report.ClassCertain), s.Code,
			})
		}

		var written strings.Builder
		if err := summary.Text(&written, r); err != nil {
			t.Fatalf("Text(%s) = %v", name, err)
		}
		got := lines(written.String())
		if len(got) != len(want) {
			t.Fatalf("Text(%s) wrote %d lines, want %d: %q", name, len(got), len(want), got)
		}
		for i, line := range got {
			m := expression.FindStringSubmatch(line)
			if m == nil {
				t.Errorf("Text(%s) line %d = %q, which the published expression refuses", name, i, line)
				continue
			}
			parsed := fields{m[1], m[2], m[3], m[4], m[5], m[6], m[7], m[8]}
			if parsed != want[i] {
				t.Errorf("Text(%s) line %d parses to %+v, want %+v", name, i, parsed, want[i])
			}
		}
	}
}

// withheldLines is every withheld line contract/grammar/text-line.md shows,
// each with the counts it names.
func withheldLines(t *testing.T) map[string]report.Withheld {
	t.Helper()

	page, err := spec.Contract.ReadFile("contract/grammar/text-line.md")
	if err != nil {
		t.Fatalf("Setup: read text-line.md: %v", err)
	}
	count := regexp.MustCompile(`([0-9]+) (probable|possible)`)
	shown := map[string]report.Withheld{}
	for _, line := range regexp.MustCompile("withheld by analysis\\.min_confidence: [^`\\n]*").FindAllString(string(page), -1) {
		var withheld report.Withheld
		for _, m := range count.FindAllStringSubmatch(line, -1) {
			n, _ := strconv.Atoi(m[1])
			if m[2] == string(report.ClassProbable) {
				withheld.Probable = n
			} else {
				withheld.Possible = n
			}
		}
		shown[line] = withheld
	}
	if len(shown) < 2 {
		t.Fatalf("Setup: text-line.md shows %d withheld lines, want the block's and the default minimum's", len(shown))
	}
	return shown
}

// A report whose totals count withheld findings ends its text with the withheld
// line the page shows for those counts, after every record's line, and the
// published expression refuses it.
func TestTextEndsWithTheWithheldLineThePageShows(t *testing.T) {
	t.Parallel()

	expression := textLine(t)
	for want, withheld := range withheldLines(t) {
		r := vector(t, "stale-suppression-carried")
		var bare strings.Builder
		if err := summary.Text(&bare, r); err != nil {
			t.Fatalf("Text = %v", err)
		}
		r.Totals.Withheld = withheld
		var written strings.Builder
		if err := summary.Text(&written, r); err != nil {
			t.Fatalf("Text(%+v) = %v", withheld, err)
		}
		if got := written.String(); got != bare.String()+want+"\n" {
			t.Errorf("Text(%+v) = %q, want the record lines then %q", withheld, got, want)
		}
		if expression.MatchString(want) {
			t.Errorf("the withheld line %q matches the finding-line expression", want)
		}
	}
}

// The merged report's withheld line names the counts the merge summed.
func TestTextOfAMergedReportNamesTheSummedWithheldCounts(t *testing.T) {
	t.Parallel()

	var written strings.Builder
	if err := summary.Text(&written, vector(t, "withheld-counts-summed")); err != nil {
		t.Fatalf("Text = %v", err)
	}
	got := lines(written.String())
	const want = "withheld by analysis.min_confidence: 3 probable, 5 possible, shown with analysis.min_confidence set to possible"
	if got[len(got)-1] != want {
		t.Errorf("Text(withheld-counts-summed) ends with %q, want %q", got[len(got)-1], want)
	}
}

// Neither the summary nor the remediation is a line the expression accepts, so
// a filter for finding lines yields the finding lines alone.
func TestTheSummaryAndTheRemediationAreNotFindingLines(t *testing.T) {
	t.Parallel()

	var written strings.Builder
	if err := summary.Write(&written, vector(t, "two-reports-no-edges")); err != nil {
		t.Fatalf("Write = %v", err)
	}
	if err := summary.Remediation(&written); err != nil {
		t.Fatalf("Remediation = %v", err)
	}
	expression := textLine(t)
	for _, line := range lines(written.String()) {
		if expression.MatchString(line) {
			t.Errorf("the line %q matches the finding-line expression, want no summary line to", line)
		}
	}
	for _, choice := range []string{"delete the symbol", "wire it up", "record an adjudication with its reason"} {
		if !strings.Contains(written.String(), choice) {
			t.Errorf("Remediation wrote %q, want it to name %q", written.String(), choice)
		}
	}
}
