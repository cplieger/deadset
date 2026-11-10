package source_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/cplieger/deadset-spec/v8/report"
	"github.com/cplieger/deadset-spec/v8/vocab"
	"github.com/cplieger/deadset/v2/internal/failure"
	"github.com/cplieger/deadset/v2/internal/rundir"
	"github.com/cplieger/deadset/v2/internal/schema"
	"github.com/cplieger/deadset/v2/internal/source"
)

const pingProto = "syntax = \"proto3\";\n\npackage v1;\n\nmessage Ping {\n  int32 number = 1;\n}\n"

// inProcessRun admits and runs the reader over a target holding one schema
// file, under config, as [source.Run] runs it.
func inProcessRun(t *testing.T, config string) (source.Outcome, rundir.Entry) {
	t.Helper()
	return inProcessRunWith(t, config, func(rundir.Entry) {})
}

// inProcessRunWith is [inProcessRun] handing a facts path, with before run
// over the entry once it is admitted.
func inProcessRunWith(t *testing.T, config string, before func(rundir.Entry)) (source.Outcome, rundir.Entry) {
	t.Helper()
	target := t.TempDir()
	if err := os.MkdirAll(filepath.Join(target, "v1"), 0o750); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(target, "v1", "ping.proto"), []byte(pingProto))
	dir, err := rundir.Create(filepath.Join(t.TempDir(), "run"))
	if err != nil {
		t.Fatal(err)
	}
	entry, err := dir.Entry(schema.Name)
	if err != nil {
		t.Fatal(err)
	}
	if err := entry.WriteConfig(t.Context(), []byte(config)); err != nil {
		t.Fatal(err)
	}
	reader := source.NewInProcess(target, ".", "example.com/app", "2.0.0", nil)
	if err := reader.Admit(t.Context(), entry, vocab.SchemaVersions()); err != nil {
		t.Fatalf("Admit = %v", err)
	}
	before(entry)
	outcomes, _ := source.Run(t.Context(), []source.Source{reader}, []source.Files{{Config: entry.Config(), Report: entry.Report(), Facts: entry.Facts()}}, false)
	return outcomes[0], entry
}

// TestInProcessWritesTheReadersReport holds the in-process source to the
// reader's report: named deadset-proto, with the embedded results'
// conformance block, this executable's digest, a target with no kind and
// the exit code 0 beside the finding an unmatched root is.
func TestInProcessWritesTheReadersReport(t *testing.T) {
	out, entry := inProcessRun(t, `{"analysis": {"min_confidence": "possible"}, "roots": {"patterns": ["proto://v1#Missing"]}}`)
	if out.Err != nil {
		t.Fatalf("Produce = %v", out.Err)
	}
	want, err := schema.Conformance()
	if err != nil {
		t.Fatal(err)
	}
	executable, _ := os.Executable()
	body, _ := os.ReadFile(executable)
	sum := sha256.Sum256(body)
	r := out.Report
	switch {
	case out.Analyzer != schema.Name || r.Analyzer.Name != schema.Name:
		t.Errorf("analyzer = %q, report analyzer %q; want %s", out.Analyzer, r.Analyzer.Name, schema.Name)
	case r.Analyzer.Conformance != want:
		t.Errorf("conformance = %+v, want %+v", r.Analyzer.Conformance, want)
	case out.Digest != "sha256:"+hex.EncodeToString(sum[:]):
		t.Errorf("digest = %s, want this executable's", out.Digest)
	case r.Target.Kind != "" || r.Target.Identity != "example.com/app":
		t.Errorf("target = %+v, want no kind and the identity handed", r.Target)
	case out.Facts == nil || len(out.Facts.Bindings) != 2:
		t.Errorf("facts = %+v, want the two records of the file", out.Facts)
	case len(r.Findings) == 0 || out.Exit != vocab.ExitClean:
		t.Errorf("exit = %d beside %d findings, want %d: the merged report, not the reader, decides the run's exit", out.Exit, len(r.Findings), vocab.ExitClean)
	}
	written, err := os.ReadFile(entry.Report())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := report.Decode(written); err != nil {
		t.Errorf("the report written is no report: %v", err)
	}
	described, err := os.ReadFile(entry.Describe())
	var document struct {
		Name                string `json:"name"`
		FactsVersionWritten string `json:"facts_version_written"`
	}
	if err == nil {
		err = json.Unmarshal(described, &document)
	}
	if err != nil || document.Name != schema.Name || document.FactsVersionWritten != vocab.FactsVersion {
		t.Errorf("describe kept = %s, %v; want the reader's describe document, writing facts %s", described, err, vocab.FactsVersion)
	}
}

// TestReplanKeepsTheAdmission holds a replanned reader to the digest and
// conformance its admission read, over the target and languages it is handed.
func TestReplanKeepsTheAdmission(t *testing.T) {
	target := t.TempDir()
	writeFile(t, filepath.Join(target, "ping.proto"), []byte(pingProto))
	dir, err := rundir.Create(filepath.Join(t.TempDir(), "run"))
	if err != nil {
		t.Fatal(err)
	}
	entry, err := dir.Entry(schema.Name)
	if err != nil {
		t.Fatal(err)
	}
	if err := entry.WriteConfig(t.Context(), []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	admitted := source.NewInProcess(target, ".", ".", "2.0.0", []vocab.Language{vocab.TS})
	if err := admitted.Admit(t.Context(), entry, vocab.SchemaVersions()); err != nil {
		t.Fatalf("Admit = %v", err)
	}
	first := admitted.Produce(t.Context(), source.Files{Config: entry.Config(), Report: filepath.Join(t.TempDir(), "first.json")})
	again := admitted.Replan(".", "example.com/app", nil).Produce(t.Context(), source.Files{Config: entry.Config(), Report: filepath.Join(t.TempDir(), "again.json")})
	if first.Err != nil || again.Err != nil {
		t.Fatalf("Produce = %v, then %v", first.Err, again.Err)
	}
	if again.Digest != first.Digest || again.Report.Analyzer.Conformance != first.Report.Analyzer.Conformance || again.Report.Target.Identity != "example.com/app" {
		t.Errorf("replanned = digest %s, conformance %+v, identity %s; want %s, %+v and example.com/app",
			again.Digest, again.Report.Analyzer.Conformance, again.Report.Target.Identity, first.Digest, first.Report.Analyzer.Conformance)
	}
}

// TestInProcessRefusesAFileAtItsOutputPaths puts a file at the report path,
// then at the facts path: the run failure unwritten-output, the file as it
// was.
func TestInProcessRefusesAFileAtItsOutputPaths(t *testing.T) {
	for _, at := range []string{"report", "facts"} {
		t.Run(at, func(t *testing.T) {
			out, entry := inProcessRunWith(t, `{}`, func(entry rundir.Entry) {
				path := map[string]string{"report": entry.Report(), "facts": entry.Facts()}[at]
				writeFile(t, path, []byte("left"))
			})
			path := map[string]string{"report": entry.Report(), "facts": entry.Facts()}[at]
			held, _ := os.ReadFile(path)
			if failures := failure.Of(out.Err); out.Err == nil || failures[0].Class != vocab.RunUnwrittenOutput || string(held) != "left" {
				t.Errorf("Produce = %v, %s holds %q; want %s and the file as it was", out.Err, at, held, vocab.RunUnwrittenOutput)
			}
		})
	}
}

// TestInProcessEndsWithTheReadersSetupFailure holds a setup failure to the
// failure code and the class line a process would print, relayed.
func TestInProcessEndsWithTheReadersSetupFailure(t *testing.T) {
	out, _ := inProcessRun(t, `{"proto": {"import_paths": ["protos"]}}`)
	if out.Err == nil || out.Exit != vocab.ExitFailure || !out.SetupFailed() || failure.Of(out.Err)[0].Class != vocab.SetupMissingImportPath {
		t.Errorf("Produce = exit %d, %v; want exit %d and the setup failure missing-import-path", out.Exit, out.Err, vocab.ExitFailure)
	}
}

// The schema reader is the reader of every in-process row: the plan names it
// by the row's analyzer and the run opens it for any in-process language, so
// a second such row needs a reader of its own before deadset can read it.
func TestTheReaderReadsEveryInProcessRow(t *testing.T) {
	t.Parallel()

	held := 0
	for _, row := range vocab.LanguageRows() {
		if !row.InProcess() {
			continue
		}
		held++
		if row.Language != vocab.Proto || row.Analyzer != schema.Name {
			t.Errorf("the in-process row of %s names %s, and this build's one reader reads %s as %s", row.Language, row.Analyzer, vocab.Proto, schema.Name)
		}
	}
	if held == 0 {
		t.Error("languages.json holds no in-process row, and this build links a schema reader")
	}
}
