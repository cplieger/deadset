package source_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/deadset-spec/v8/report"
	"github.com/cplieger/deadset-spec/v8/vocab"
	"github.com/cplieger/deadset/v2/internal/config"
	"github.com/cplieger/deadset/v2/internal/plan"
	"github.com/cplieger/deadset/v2/internal/source"
)

// readError is the *source.Error an outcome refused with.
func readError(t *testing.T, out *source.Outcome) *source.Error {
	t.Helper()

	refused, ok := errors.AsType[*source.Error](out.Err)
	if !ok || out.Report != nil {
		t.Fatalf("Produce() = %+v, want no report and a *source.Error", out)
	}
	return refused
}

// Each exit code that is a verdict about a complete report lets the report be
// read, the analyze verb having run with the three paths the analyzer CLI
// contract passes in the source's directory.
func TestProduceReadsTheReportOfEveryVerdict(t *testing.T) {
	t.Parallel()

	written := published(t, goReport)
	want, err := report.Decode(written)
	if err != nil {
		t.Fatalf("Setup: decode %s: %v", goReport, err)
	}
	for _, exit := range []int{0, 1, 4} {
		t.Run(strconv.Itoa(exit), func(t *testing.T) {
			t.Parallel()

			analyzer := fakeAnalyzer(t, nil, exit, written)
			planned := &plan.Source{Entry: config.Provider{Name: "deadset-go", Command: analyzer.command}, Languages: []vocab.Language{vocab.Go}}
			dir := t.TempDir()
			p, err := source.Open(planned, dir, nil)
			if err != nil {
				t.Fatalf("Setup: Open() = %v", err)
			}
			paths := files(t)
			got := p.Produce(t.Context(), paths)
			if got.Err != nil || !reflect.DeepEqual(got.Report, want) || got.Exit != exit || got.Analyzer != "deadset-go" {
				t.Fatalf("Produce(an analyzer exiting %d) = %+v, want its report", exit, got)
			}
			recorded, _ := os.ReadFile(analyzer.args)
			args := strings.Split(strings.TrimSuffix(string(recorded), "\n"), "\n")
			if wantArgs := []string{"analyze", "--scope=" + paths.Scope, "--config=" + paths.Config, "--report=" + paths.Report}; !slices.Equal(args, wantArgs) {
				t.Errorf("Produce() ran the analyzer with %q, want %q", args, wantArgs)
			}
			if ranIn, _ := os.ReadFile(analyzer.dir); strings.TrimSpace(string(ranIn)) != dir {
				t.Errorf("Produce() ran the analyzer in %q, want %s", ranIn, dir)
			}
		})
	}
}

// The exit code is read before the report is opened: an analyzer that
// refused its invocation, failed, or exited with a code the contract does not
// define is refused with its code, and what it left is never decoded. Only
// exit 2 is named the analyzer's usage or configuration error, and what it
// printed is held whole.
func TestProduceReadsNoReportAfterAnExitThatCarriesNone(t *testing.T) {
	t.Parallel()

	written := published(t, goReport)
	for _, exit := range []int{2, 3, 5} {
		t.Run(strconv.Itoa(exit), func(t *testing.T) {
			t.Parallel()

			analyzer := script(t, t.TempDir(), fmt.Sprintf("echo 'to stdout'\necho 'to stderr' >&2\nprintf '%%s' '%s' > \"$report\"\nexit %d\n", written[:len(written)/2], exit))
			got := opened(t, "deadset-go", analyzer.command, nil).Produce(t.Context(), files(t))
			refused := readError(t, &got)
			if !errors.Is(got.Err, source.ErrExited) || refused.Exit != exit || errors.Is(got.Err, report.ErrInvalid) {
				t.Errorf("Produce(an analyzer exiting %d) = %v, want ErrExited with exit %d and no decode", exit, got.Err, exit)
			}
			if named := strings.Contains(got.Err.Error(), "a usage or configuration error of the analyzer"); named != (exit == vocab.ExitUsage) {
				t.Errorf("Produce(an analyzer exiting %d) = %q, want it named a usage or configuration error: %t", exit, got.Err, exit == 2)
			}
			if string(got.Printed) != "to stdout\nto stderr\n" {
				t.Errorf("Produce() held %q as printed, want both streams", got.Printed)
			}
		})
	}
}

// Every read Produce refuses after a verdict, and the one it refuses before
// running anything, each naming the entry and the report path.
func TestProduceRefusesAReportItCannotUse(t *testing.T) {
	t.Parallel()

	written := published(t, goReport)
	for _, c := range []struct {
		name    string
		written []byte
		before  bool
		want    error
		names   string
	}{
		{name: "absent", want: source.ErrNoReport},
		{name: "truncated", written: written[:len(written)/2], want: report.ErrInvalid},
		{name: "another-analyzer", written: published(t, tsReport), names: `the report names the analyzer "deadset-ts"`},
		{name: "already-at-the-path", written: written, before: true, want: source.ErrReportExists},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			analyzer := fakeAnalyzer(t, nil, 1, c.written)
			paths := files(t)
			if c.before {
				writeFile(t, paths.Report, written)
			}
			got := opened(t, "deadset-go", analyzer.command, nil).Produce(t.Context(), paths)
			refused := readError(t, &got)
			if c.want != nil && !errors.Is(got.Err, c.want) {
				t.Errorf("Produce(%s) = %v, want %v", c.name, got.Err, c.want)
			}
			for _, named := range []string{"deadset-go", paths.Report, c.names} {
				if !strings.Contains(got.Err.Error(), named) {
					t.Errorf("Produce(%s) = %q, want it to name %q", c.name, got.Err, named)
				}
			}
			_, ran := os.Stat(analyzer.args)
			if c.before != errors.Is(ran, fs.ErrNotExist) || (c.before && refused.Exit != -1) {
				t.Errorf("Produce(%s) ran the analyzer: %t, exit %d, want it run only over no earlier report", c.name, ran == nil, refused.Exit)
			}
		})
	}
}

// Cancelling the run interrupts the analyzer rather than killing it, and the
// report and the verdict it answers the interrupt with are not read.
func TestProduceInterruptsTheAnalyzerOfACancelledRun(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	document := filepath.Join(dir, "written.json")
	writeFile(t, document, published(t, goReport))
	started, interrupted := filepath.Join(dir, "started"), filepath.Join(dir, "interrupted")
	analyzer := script(t, dir, strings.Join([]string{
		"document='" + document + "'",
		"interrupted='" + interrupted + "'",
		"sleep 60 & sleeper=$!",
		`trap 'kill "$sleeper"; cp "$document" "$report"; : > "$interrupted"; exit 1' INT`,
		": > '" + started + "'",
		`wait "$sleeper"`,
	}, "\n"))

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() {
		defer cancel()
		for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			if _, err := os.Stat(started); err == nil {
				return
			}
		}
	}()
	got := opened(t, "deadset-go", analyzer.command, nil).Produce(ctx, files(t))
	if refused := readError(t, &got); !errors.Is(got.Err, context.Canceled) || refused.Exit != 1 {
		t.Errorf("Produce(a cancelled run) = %v, exit %d, want context.Canceled with the exit code it answered with, 1", got.Err, refused.Exit)
	}
	if _, err := os.Stat(interrupted); err != nil {
		t.Errorf("after Produce(a cancelled run), Stat(%s) = %v, want the analyzer interrupted", interrupted, err)
	}
}

// A command is resolved once, when the source opens: a bare name on PATH, an
// absolute path as it is, and a relative path or a name nothing resolves is a
// *CommandError naming the entry and its place in the provider list.
func TestOpenResolvesTheCommandOnce(t *testing.T) {
	analyzer := fakeAnalyzer(t, nil, 0, nil)
	t.Setenv("PATH", filepath.Dir(analyzer.command))
	for _, c := range []struct {
		command string
		want    error
	}{
		{command: "analyzer"},
		{command: analyzer.command},
		{command: "bin/analyzer", want: source.ErrRelativeCommand},
		{command: "deadset-no-path-holds", want: exec.ErrNotFound},
	} {
		planned := &plan.Source{Entry: config.Provider{Name: "deadset-go", Command: c.command}, Index: 2}
		p, err := source.Open(planned, t.TempDir(), nil)
		if c.want == nil {
			if err != nil || p.Name() != "deadset-go" {
				t.Errorf("Open(%s) = %v, %v, want the source", c.command, p, err)
			}
			continue
		}
		refused, ok := errors.AsType[*source.CommandError](err)
		if p != nil || !ok || !errors.Is(err, c.want) || refused.Index != 2 || !strings.Contains(err.Error(), "providers.analyzers[2]") {
			t.Errorf("Open(%s) = %v, %v, want a *CommandError naming providers.analyzers[2] and %v", c.command, p, err, c.want)
		}
	}
}

// describedAs is a describe document naming name and languages, reading
// versions, with a conformance record of result.
func describedAs(name, languages, versions, result string) []byte {
	conformance := ""
	if result != "" {
		conformance = `, "conformance": {"corpus_version": "1.9.0", "result": "` + result + `", "digest": "sha256:` + strings.Repeat("0a", 32) + `"}`
	}
	return []byte(`{"name": "` + name + `", "version": "1.20.0", "contract_version": "8.0.0", "schema_versions_accepted": ` + versions +
		`, "languages": ` + languages + conformance + "}\n")
}

// The admitted handshake: the describe verb run alone, the printed document
// kept byte for byte without the analyzer's stderr, which goes to the
// diagnostics, and an analyzer admitted when one of several versions it reads
// is accepted.
func TestAdmitAdmitsAnAnalyzerWithAPassAndAnAcceptedVersion(t *testing.T) {
	t.Parallel()

	printed := describedAs("deadset-go", `["go", "ts"]`, `["5.0.0", "`+vocab.SchemaVersion+`"]`, "pass")
	analyzer := fakeAnalyzer(t, printed, 0, nil)
	var diagnostics bytes.Buffer
	entry := kept(t, "deadset-go")
	if err := opened(t, "deadset-go", analyzer.command, &diagnostics, vocab.Go).Admit(t.Context(), entry, []string{vocab.SchemaVersion}); err != nil {
		t.Fatalf("Admit() = %v, want the analyzer admitted", err)
	}
	if keptBytes, err := os.ReadFile(entry.Describe()); err != nil || !bytes.Equal(keptBytes, printed) {
		t.Errorf("Admit() kept %q (%v), want the bytes printed, %q", keptBytes, err, printed)
	}
	if diagnostics.String() != "deadset-go: a diagnostic\n" {
		t.Errorf("Admit() copied %q to the diagnostics, want the analyzer's stderr under its name", diagnostics.String())
	}
	if args, _ := os.ReadFile(analyzer.args); string(args) != "describe\n" {
		t.Errorf("Admit() ran the analyzer with %q, want the describe verb alone", args)
	}
}

// Every refused handshake is a *HandshakeError naming the entry and why.
func TestAdmitRefuses(t *testing.T) {
	t.Parallel()

	accepted := `["` + vocab.SchemaVersion + `"]`
	for _, c := range []struct {
		name    string
		printed []byte
		exit    int
		want    error
		names   []string
	}{
		{name: "another-name", printed: describedAs("deadset-go-fork", `["go"]`, accepted, "pass"), want: source.ErrDescribedName, names: []string{`"deadset-go-fork"`}},
		{name: "a-language-unclaimed", printed: describedAs("deadset-go", `["ts"]`, accepted, "pass"), want: source.ErrDescribedLanguages, names: []string{"hands it go"}},
		{name: "failing-conformance", printed: describedAs("deadset-go", `["go"]`, accepted, "fail"), want: source.ErrNoConformancePass, names: []string{`"fail"`, "1.9.0"}},
		{name: "no-conformance", printed: describedAs("deadset-go", `["go"]`, accepted, ""), want: source.ErrNoConformancePass, names: []string{"no conformance record"}},
		{name: "schema-version", printed: describedAs("deadset-go", `["go"]`, `["5.0.0", "6.1.0"]`, "pass"), want: source.ErrSchemaVersion, names: []string{"5.0.0, 6.1.0", vocab.SchemaVersion}},
		{name: "document-refused", printed: []byte(`{"name": "deadset-go", "schema_version": "x"}`), want: source.ErrDescription, names: []string{"the document: additionalProperties"}},
		{name: "not-clean", printed: describedAs("deadset-go", `["go"]`, accepted, "pass"), exit: 2, want: source.ErrDescribeExited, names: []string{"exited 2"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			describedFile := filepath.Join(dir, "described.json")
			writeFile(t, describedFile, c.printed)
			analyzer := script(t, dir, fmt.Sprintf("cat '%s'\nexit %d\n", describedFile, c.exit))
			entry := kept(t, "deadset-go")
			err := opened(t, "deadset-go", analyzer.command, nil, vocab.Go).Admit(t.Context(), entry, []string{vocab.SchemaVersion})
			if _, ok := errors.AsType[*source.HandshakeError](err); !ok || !errors.Is(err, c.want) {
				t.Fatalf("Admit(%s) = %v, want a *HandshakeError satisfying errors.Is(err, %v)", c.name, err, c.want)
			}
			for _, named := range append([]string{"deadset-go"}, c.names...) {
				if !strings.Contains(err.Error(), named) {
					t.Errorf("Admit(%s) = %q, want it to name %q", c.name, err, named)
				}
			}
			if keptBytes, _ := os.ReadFile(entry.Describe()); !bytes.Equal(keptBytes, c.printed) {
				t.Errorf("Admit(%s) kept %q, want the bytes it printed", c.name, keptBytes)
			}
		})
	}
}

func TestAdmitShowsTheDiagnosticsUnderTheAnalyzersName(t *testing.T) {
	t.Parallel()

	held := map[string]string{"single-prefix": "run failure: product-fault: describe broke\n"}
	maps.Copy(held, forgeries)
	for name, printed := range held {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "printed"), []byte(printed))
		analyzer := script(t, dir, fmt.Sprintf("cat '%s/printed' >&2\nexit 3\n", dir))
		var diagnostics bytes.Buffer
		err := opened(t, "deadset-go", analyzer.command, &diagnostics, vocab.Go).Admit(t.Context(), kept(t, "deadset-go"), []string{vocab.SchemaVersion})
		if lines := classLines(diagnostics.String()); !errors.Is(err, source.ErrDescribeExited) || len(lines) != 0 {
			t.Errorf("Admit(%s) = %v, diagnostics %q, want ErrDescribeExited and no class line, got %q", name, err, diagnostics.String(), lines)
		}
		if name == "single-prefix" && diagnostics.String() != "deadset-go: run failure: product-fault: describe broke\n" {
			t.Errorf("Admit(%s) diagnostics %q, want the line under the analyzer's name", name, diagnostics.String())
		}
	}
}

// A command that never starts is refused naming it, and keeps nothing.
func TestAdmitRefusesACommandItCannotRun(t *testing.T) {
	t.Parallel()

	entry := kept(t, "deadset-go")
	analyzer := fakeAnalyzer(t, nil, 0, nil)
	gone := opened(t, "deadset-go", analyzer.command, nil)
	if err := os.Remove(analyzer.command); err != nil {
		t.Fatalf("Setup: remove %s: %v", analyzer.command, err)
	}
	err := gone.Admit(t.Context(), entry, []string{vocab.SchemaVersion})
	if _, ok := errors.AsType[*source.HandshakeError](err); !ok || !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Admit(a command removed since it was resolved) = %v, want a *HandshakeError satisfying fs.ErrNotExist", err)
	}
	if _, err := os.Stat(entry.Describe()); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Admit() kept a describe file: %v, want nothing kept", err)
	}
}
