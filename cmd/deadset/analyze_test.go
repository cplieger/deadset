package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"

	spec "github.com/cplieger/deadset-spec/v3"
	"github.com/cplieger/deadset/internal/report"
	"github.com/cplieger/deadset/internal/verdict"
)

// The analyzer reports the fakes write, each a published merge input.
const (
	goFindings = "vectors/merge/one-report/inputs/00-go.json"
	goPending  = "vectors/merge/pending-pair-unevaluated/inputs/00-go.json"
	tsFindings = "vectors/merge/two-reports-no-edges/inputs/01-ts.json"
	goWarnOnly = "vectors/merge/fail-on-warn/inputs/00-go.json"
)

// fake is an executable standing in for one analyzer, and the files it
// leaves: the directory its analyze verb ran in, and a copy of the scope
// document it was handed.
type fake struct {
	command string
	ranIn   string
	scope   string
}

// fakeAnalyzer writes an analyzer named name, claiming languages, whose
// describe verb describes it with a conformance pass and whose analyze verb
// records the directory it runs in and the scope document it is handed,
// copies the published report written to the path --report names unless
// written is empty, and exits with exit.
func fakeAnalyzer(t *testing.T, name string, languages []string, exit int, written string) fake {
	t.Helper()

	dir := t.TempDir()
	described, err := json.Marshal(map[string]any{
		"name":                     name,
		"version":                  "1.0.0",
		"contract_version":         report.ContractVersion,
		"schema_versions_accepted": []string{report.SchemaVersion},
		"languages":                languages,
		"conformance":              map[string]string{"corpus_version": "1.9.0", "result": "pass", "digest": "sha256:" + strings.Repeat("0a", 32)},
	})
	if err != nil {
		t.Fatalf("Setup: encode the describe document: %v", err)
	}
	writeFile(t, filepath.Join(dir, "described.json"), described)
	copied := ""
	if written != "" {
		body, err := spec.Vectors.ReadFile(written)
		if err != nil {
			t.Fatalf("Setup: read %s: %v", written, err)
		}
		writeFile(t, filepath.Join(dir, "written.json"), body)
		copied = fmt.Sprintf(`cp '%s/written.json' "$report"`, dir)
	}
	f := fake{command: filepath.Join(dir, "analyzer"), ranIn: filepath.Join(dir, "pwd"), scope: filepath.Join(dir, "scope.json")}
	script := strings.Join([]string{
		"#!/bin/sh",
		fmt.Sprintf(`if [ "$1" = describe ]; then cat '%s/described.json'; exit 0; fi`, dir),
		fmt.Sprintf(`pwd > '%s'`, f.ranIn),
		`for argument in "$@"; do`,
		`	case "$argument" in`,
		`	--report=*) report="${argument#--report=}" ;;`,
		fmt.Sprintf(`	--scope=*) cp "${argument#--scope=}" '%s' ;;`, f.scope),
		`	esac`,
		`done`,
		copied,
		"exit " + strconv.Itoa(exit),
		"",
	}, "\n")
	// A fork in a parallel test inherits the descriptor the script is written
	// through, and exec then fails with ETXTBSY until that child execs; a fork
	// waits for every ForkLock reader.
	syscall.ForkLock.RLock()
	err = os.WriteFile(f.command, []byte(script), 0o700)
	syscall.ForkLock.RUnlock()
	if err != nil {
		t.Fatalf("Setup: write %s: %v", f.command, err)
	}
	return f
}

// writeFile writes one file a test's setup needs.
func writeFile(t *testing.T, path string, body []byte) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("Setup: create the directory of %s: %v", path, err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("Setup: write %s: %v", path, err)
	}
}

// provider is one entry of a provider list.
type provider struct {
	Name      string   `json:"name"`
	Command   string   `json:"command"`
	Languages []string `json:"languages"`
}

// goTarget is a target tree holding a Go module and the repository
// configuration naming the target kind, the given provider list and any extra
// members, and the path of a run directory beside it that does not exist yet.
func goTarget(t *testing.T, providers []provider, extra string) (target, runDir string) {
	t.Helper()

	base := t.TempDir()
	target = filepath.Join(base, "target")
	list, err := json.Marshal(providers)
	if err != nil {
		t.Fatalf("Setup: encode the provider list: %v", err)
	}
	writeFile(t, filepath.Join(target, "go.mod"), []byte("module example.com/app\n\ngo 1.27\n"))
	writeFile(t, filepath.Join(target, "deadset.json"),
		[]byte(`{"target": {"kind": "application"}, "providers": {"analyzers": `+string(list)+`}`+extra+`}`))
	return target, filepath.Join(base, "run")
}

// analyzeRun is what one analyze invocation returned and printed.
type analyzeRun struct {
	stdout, stderr string
	code           int
}

// analyze runs the analyze verb over target into runDir with extra flags.
func analyze(t *testing.T, target, runDir string, extra ...string) analyzeRun {
	t.Helper()

	var stdout, stderr bytes.Buffer
	args := append([]string{"analyze", "--target=" + target, "--run-dir=" + runDir}, extra...)
	code := run(args, &stdout, &stderr)
	return analyzeRun{stdout: stdout.String(), stderr: stderr.String(), code: code}
}

// mergedReport decodes the merged report a run wrote with the closed-key
// decode the report schemas are pinned to.
func mergedReport(t *testing.T, runDir string) *report.Report {
	t.Helper()

	body, err := os.ReadFile(filepath.Join(runDir, "report.json"))
	if err != nil {
		t.Fatalf("read the merged report: %v", err)
	}
	r, err := report.Decode(body)
	if err != nil {
		t.Fatalf("Decode(the merged report) = %v, want a report the schemas admit", err)
	}
	return r
}

// listing is the name of every file in dir.
func listing(t *testing.T, dir string) []string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", dir, err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

// A run keeps every file of its evidence keyed by each entry's name, writes
// the merged report, prints its lines, the summary and the remediation, and
// exits with the merged report's verdict. The scope's working directory and
// the directory each analyzer ran in are one value, the target root.
func TestAnalyzeKeepsTheEvidenceAndExitsWithTheMergedVerdict(t *testing.T) {
	t.Parallel()

	goFake := fakeAnalyzer(t, "deadset-go", []string{"go"}, 1, goFindings)
	tsFake := fakeAnalyzer(t, "deadset-ts", []string{"ts"}, 1, tsFindings)
	target, runDir := goTarget(t, []provider{
		{Name: "deadset-go", Command: goFake.command, Languages: []string{"go"}},
		{Name: "deadset-ts", Command: tsFake.command, Languages: []string{"ts"}},
	}, "")
	writeFile(t, filepath.Join(target, "tsconfig.json"), []byte("{}\n"))

	got := analyze(t, target, runDir)
	if got.code != verdict.Findings {
		t.Fatalf("analyze = %d, want %d\nstderr: %s", got.code, verdict.Findings, got.stderr)
	}
	want := []string{
		"config.deadset-go.json", "config.deadset-ts.json", "describe.deadset-go.json", "describe.deadset-ts.json",
		"report.deadset-go.json", "report.deadset-ts.json", "report.json", "scope.json",
	}
	if names := listing(t, runDir); !slices.Equal(names, want) {
		t.Errorf("the run directory holds %q, want %q", names, want)
	}
	if !strings.Contains(got.stderr, "the run directory is "+runDir) {
		t.Errorf("analyze stderr = %q, want it to name the run directory", got.stderr)
	}

	var scope struct {
		Target struct {
			Path string `json:"path"`
		} `json:"target"`
	}
	body, err := os.ReadFile(filepath.Join(runDir, "scope.json"))
	if err != nil || json.Unmarshal(body, &scope) != nil {
		t.Fatalf("read scope.json: %v", err)
	}
	for _, f := range []fake{goFake, tsFake} {
		ranIn, err := os.ReadFile(f.ranIn)
		if err != nil {
			t.Fatalf("read where %s ran: %v", f.command, err)
		}
		if dir := strings.TrimSpace(string(ranIn)); dir != target || dir != scope.Target.Path {
			t.Errorf("%s ran in %s and the scope names the target %s, want both the target root %s", f.command, dir, scope.Target.Path, target)
		}
	}

	merged := mergedReport(t, runDir)
	if merged.Analyzer.Name != name || merged.Analyzer.Conformance.Result != report.ResultPass {
		t.Errorf("the merged report's analyzer = %+v, want %s with a conformance pass", merged.Analyzer, name)
	}
	for i, f := range []fake{goFake, tsFake} {
		body, err := os.ReadFile(f.command)
		if err != nil {
			t.Fatalf("Setup: read %s: %v", f.command, err)
		}
		sum := sha256.Sum256(body)
		if digest := "sha256:" + hex.EncodeToString(sum[:]); merged.MergedFrom[i].Digest != digest {
			t.Errorf("merged_from[%d] = %+v, want the digest of %s, %s", i, merged.MergedFrom[i], f.command, digest)
		}
	}
	if merged.Totals.Findings != 3 || len(merged.Findings) != 3 {
		t.Errorf("the merged report holds %d findings and counts %d, want both reports' 3", len(merged.Findings), merged.Totals.Findings)
	}
	lines := strings.Split(strings.TrimSuffix(got.stdout, "\n"), "\n")
	if len(lines) != 3+2+1+1 || !strings.HasPrefix(lines[0], "internal/store/store.go:41:6: ") ||
		!strings.HasPrefix(lines[3], "analyzer deadset-go ") ||
		!strings.HasPrefix(lines[5], "summary: 3 findings") || !strings.HasPrefix(lines[6], "remediation: ") {
		t.Errorf("analyze stdout =\n%s\nwant 3 finding lines, the 2 analyzer lines, the summary and the remediation", got.stdout)
	}
}

// Each analyzer's configuration holds the section of each language in scope
// it claims and no other, whatever else its entry claims.
func TestAnalyzeHandsEachAnalyzerTheSectionsOfItsLanguagesInScope(t *testing.T) {
	t.Parallel()

	both := fakeAnalyzer(t, "deadset-go", []string{"go", "ts"}, 1, goFindings)
	target, runDir := goTarget(t, []provider{{Name: "deadset-go", Command: both.command, Languages: []string{"go", "ts"}}},
		`, "ts": {"entry_files": ["src/cli.ts"]}`)

	if got := analyze(t, target, runDir); got.code != verdict.Findings {
		t.Fatalf("analyze = %d, want %d\nstderr: %s", got.code, verdict.Findings, got.stderr)
	}
	var handed map[string]json.RawMessage
	body, err := os.ReadFile(filepath.Join(runDir, "config.deadset-go.json"))
	if err != nil || json.Unmarshal(body, &handed) != nil {
		t.Fatalf("read config.deadset-go.json: %v", err)
	}
	_, goSection := handed["go"]
	_, tsSection := handed["ts"]
	_, providerList := handed["providers"]
	if !goSection || tsSection || providerList {
		t.Errorf("config.deadset-go.json holds go %t, ts %t, providers %t, want the go section alone of the three", goSection, tsSection, providerList)
	}
}

// Every way a run ends before or after its merged report exists, each with
// its exit code and what it names.
func TestAnalyzeExitCodes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		setup func(t *testing.T) (target, runDir string, extra []string)
		code  int
		names []string
	}{
		{
			name: "findings-exit-code-off",
			setup: func(t *testing.T) (string, string, []string) {
				f := fakeAnalyzer(t, "deadset-go", []string{"go"}, 1, goFindings)
				target, runDir := goTarget(t, []provider{{Name: "deadset-go", Command: f.command, Languages: []string{"go"}}}, "")
				return target, runDir, []string{"--exit-code=off"}
			},
			code:  verdict.Clean,
			names: []string{"the verdict of this run is 1"},
		},
		{
			name: "exit-code-value-outside-its-set",
			setup: func(t *testing.T) (string, string, []string) {
				return t.TempDir(), filepath.Join(t.TempDir(), "run"), []string{"--exit-code=maybe"}
			},
			code:  verdict.Usage,
			names: []string{"--exit-code", `"maybe"`, "on and off", "usage: deadset analyze"},
		},
		{
			name: "argument",
			setup: func(t *testing.T) (string, string, []string) {
				return t.TempDir(), filepath.Join(t.TempDir(), "run"), []string{"extra"}
			},
			code:  verdict.Usage,
			names: []string{`"extra"`},
		},
		{
			name: "no-target-kind",
			setup: func(t *testing.T) (string, string, []string) {
				target := t.TempDir()
				writeFile(t, filepath.Join(target, "go.mod"), []byte("module example.com/app\n"))
				return target, filepath.Join(t.TempDir(), "run"), nil
			},
			code:  verdict.Usage,
			names: []string{"target.kind"},
		},
		{
			name: "missing-target",
			setup: func(t *testing.T) (string, string, []string) {
				return filepath.Join(t.TempDir(), "absent"), filepath.Join(t.TempDir(), "run"), nil
			},
			code:  verdict.Failure,
			names: []string{"the target"},
		},
		{
			name: "missing-central-configuration",
			setup: func(t *testing.T) (string, string, []string) {
				return t.TempDir(), filepath.Join(t.TempDir(), "run"), []string{"--central=" + filepath.Join(t.TempDir(), "central.json")}
			},
			code:  verdict.Usage,
			names: []string{"--central", "central.json"},
		},
		{
			name: "template-format-without-a-template",
			setup: func(t *testing.T) (string, string, []string) {
				f := fakeAnalyzer(t, "deadset-go", []string{"go"}, 1, goFindings)
				target, runDir := goTarget(t, []provider{{Name: "deadset-go", Command: f.command, Languages: []string{"go"}}}, "")
				return target, runDir, []string{"--formats=text,template"}
			},
			code:  verdict.Usage,
			names: []string{"--template", "template format"},
		},
		{
			name: "template-that-does-not-parse",
			setup: func(t *testing.T) (string, string, []string) {
				f := fakeAnalyzer(t, "deadset-go", []string{"go"}, 1, goFindings)
				target, runDir := goTarget(t, []provider{{Name: "deadset-go", Command: f.command, Languages: []string{"go"}}}, "")
				template := filepath.Join(t.TempDir(), "report.tmpl")
				writeFile(t, template, []byte("{{range .Findings}}"))
				return target, runDir, []string{"--formats=template", "--template=" + template}
			},
			code:  verdict.Usage,
			names: []string{"--template", "parse the template"},
		},
		{
			name: "report-under-another-analyzer-name",
			setup: func(t *testing.T) (string, string, []string) {
				f := fakeAnalyzer(t, "deadset-go-fork", []string{"go"}, 1, goFindings)
				target, runDir := goTarget(t, []provider{{Name: "deadset-go-fork", Command: f.command, Languages: []string{"go"}}}, "")
				return target, runDir, nil
			},
			code:  verdict.Failure,
			names: []string{"deadset-go-fork", `"deadset-go"`, "report.deadset-go-fork.json"},
		},
		{
			name: "unclaimed-language",
			setup: func(t *testing.T) (string, string, []string) {
				f := fakeAnalyzer(t, "deadset-ts", []string{"ts"}, 0, tsFindings)
				target, runDir := goTarget(t, []provider{{Name: "deadset-ts", Command: f.command, Languages: []string{"ts"}}}, "")
				return target, runDir, nil
			},
			code:  verdict.Usage,
			names: []string{"providers.analyzers", `"go"`},
		},
		{
			name: "existing-run-directory",
			setup: func(t *testing.T) (string, string, []string) {
				f := fakeAnalyzer(t, "deadset-go", []string{"go"}, 1, goFindings)
				target, _ := goTarget(t, []provider{{Name: "deadset-go", Command: f.command, Languages: []string{"go"}}}, "")
				return target, t.TempDir(), nil
			},
			code:  verdict.Usage,
			names: []string{"--run-dir"},
		},
		{
			name: "ts-analyzer-command-absent",
			setup: func(t *testing.T) (string, string, []string) {
				f := fakeAnalyzer(t, "deadset-go", []string{"go"}, 1, goFindings)
				target, runDir := goTarget(t, []provider{
					{Name: "deadset-go", Command: f.command, Languages: []string{"go"}},
					{Name: "deadset-ts", Command: "deadset-ts-no-path-holds", Languages: []string{"ts"}},
				}, "")
				writeFile(t, filepath.Join(target, "tsconfig.json"), []byte("{}\n"))
				return target, runDir, nil
			},
			code:  verdict.Failure,
			names: []string{"deadset-ts", "providers.analyzers[1]", "deadset-ts-no-path-holds"},
		},
		{
			name: "described-under-another-name",
			setup: func(t *testing.T) (string, string, []string) {
				f := fakeAnalyzer(t, "deadset-go", []string{"go"}, 1, goFindings)
				target, runDir := goTarget(t, []provider{{Name: "deadset-go-fork", Command: f.command, Languages: []string{"go"}}}, "")
				return target, runDir, nil
			},
			code:  verdict.Failure,
			names: []string{"deadset-go-fork", `"deadset-go"`},
		},
		{
			name: "analyzer-refusing-its-configuration",
			setup: func(t *testing.T) (string, string, []string) {
				f := fakeAnalyzer(t, "deadset-go", []string{"go"}, 2, "")
				target, runDir := goTarget(t, []provider{{Name: "deadset-go", Command: f.command, Languages: []string{"go"}}}, "")
				return target, runDir, nil
			},
			code:  verdict.Usage,
			names: []string{"deadset-go", "exited 2"},
		},
		{
			name: "analyzer-failing",
			setup: func(t *testing.T) (string, string, []string) {
				goFake := fakeAnalyzer(t, "deadset-go", []string{"go"}, 1, goFindings)
				tsFake := fakeAnalyzer(t, "deadset-ts", []string{"ts"}, 3, "")
				target, runDir := goTarget(t, []provider{
					{Name: "deadset-go", Command: goFake.command, Languages: []string{"go"}},
					{Name: "deadset-ts", Command: tsFake.command, Languages: []string{"ts"}},
				}, "")
				writeFile(t, filepath.Join(target, "tsconfig.json"), []byte("{}\n"))
				return target, runDir, nil
			},
			code:  verdict.Failure,
			names: []string{"deadset-ts", "exited 3"},
		},
		{
			name: "pending-pair-unevaluated",
			setup: func(t *testing.T) (string, string, []string) {
				f := fakeAnalyzer(t, "deadset-go", []string{"go"}, 4, goPending)
				target, runDir := goTarget(t, []provider{{Name: "deadset-go", Command: f.command, Languages: []string{"go"}}}, "")
				return target, runDir, nil
			},
			code:  verdict.Failure,
			names: []string{"wire/ServerEvent", "provides", "go://example.com/app/internal/wire#ServerEvent"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			target, runDir, extra := c.setup(t)
			got := analyze(t, target, runDir, extra...)
			if got.code != c.code {
				t.Errorf("analyze(%s) = %d, want %d\nstderr: %s", c.name, got.code, c.code, got.stderr)
			}
			for _, named := range c.names {
				if !strings.Contains(got.stderr, named) {
					t.Errorf("analyze(%s) stderr = %q, want it to name %s", c.name, got.stderr, named)
				}
			}
			produced := c.code == verdict.Clean || c.code == verdict.Findings
			if _, err := os.Stat(filepath.Join(runDir, "report.json")); (err == nil) != produced {
				t.Errorf("analyze(%s) left a merged report: %t, want %t", c.name, err == nil, produced)
			}
			if !produced && got.stdout != "" {
				t.Errorf("analyze(%s) stdout = %q, want nothing presented as the run's result", c.name, got.stdout)
			}
		})
	}
}

// A warn finding fails the run only at or below a failing severity of warn, and
// only a failing run prints the remediation; the annotations follow the same
// failing severity.
func TestAnalyzeFailsOnAFindingAtOrAboveTheFailingSeverity(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		failOn, annotation string
		code               int
		remediation        bool
	}{
		{failOn: "deny", annotation: "::warning ", code: verdict.Clean},
		{failOn: "warn", annotation: "::error ", code: verdict.Findings, remediation: true},
	} {
		t.Run(c.failOn, func(t *testing.T) {
			t.Parallel()

			f := fakeAnalyzer(t, "deadset-go", []string{"go"}, 1, goWarnOnly)
			target, runDir := goTarget(t, []provider{{Name: "deadset-go", Command: f.command, Languages: []string{"go"}}}, "")
			got := analyze(t, target, runDir, "--fail-on="+c.failOn, "--formats=github,text")
			if got.code != c.code {
				t.Errorf("analyze(--fail-on=%s) = %d, want %d\nstderr: %s", c.failOn, got.code, c.code, got.stderr)
			}
			if !strings.HasPrefix(got.stdout, c.annotation) {
				t.Errorf("analyze(--fail-on=%s) stdout = %q, want it to open with a %q annotation", c.failOn, got.stdout, c.annotation)
			}
			if strings.Contains(got.stdout, "remediation: ") != c.remediation {
				t.Errorf("analyze(--fail-on=%s) stdout = %q, want the remediation printed: %t", c.failOn, got.stdout, c.remediation)
			}
		})
	}
}

// sourceFiles writes, below target, every source file the published reports
// the fakes copy name, each long enough to hold the line its finding names.
func sourceFiles(t *testing.T, target string) {
	t.Helper()

	for _, path := range []string{"internal/store/store.go", "web/src/tabs.ts", "web/src/wire.ts"} {
		var lines strings.Builder
		for line := range 64 {
			fmt.Fprintf(&lines, "// %s line %d\n", path, line+1)
		}
		writeFile(t, filepath.Join(target, filepath.FromSlash(path)), []byte(lines.String()))
	}
}

// sarifLog is the members of a SARIF log a run's rendering is checked by.
type sarifLog struct {
	Runs []struct {
		Tool struct {
			Driver struct {
				Name string `json:"name"`
			} `json:"driver"`
		} `json:"tool"`
		Results []struct {
			RuleID    string `json:"ruleId"`
			Locations []struct {
				PhysicalLocation struct {
					ArtifactLocation struct {
						URI string `json:"uri"`
					} `json:"artifactLocation"`
				} `json:"physicalLocation"`
			} `json:"locations"`
			PartialFingerprints map[string]string `json:"partialFingerprints"`
		} `json:"results"`
	} `json:"runs"`
}

// readSARIF decodes the SARIF log at path.
func readSARIF(t *testing.T, path string) *sarifLog {
	t.Helper()

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var log sarifLog
	if err := json.Unmarshal(body, &log); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return &log
}

// A run naming the sarif and template formats writes each beside the merged
// report and names both paths after the summary. The SARIF log holds one run
// per analyzer with a result per finding it carried, its line fingerprints
// read from the target root rather than the directory the run was started
// in, and the template renders the findings the text lines print.
func TestAnalyzeWritesTheSARIFAndTemplateRenderingsBesideTheMergedReport(t *testing.T) {
	t.Parallel()

	goFake := fakeAnalyzer(t, "deadset-go", []string{"go"}, 1, goFindings)
	tsFake := fakeAnalyzer(t, "deadset-ts", []string{"ts"}, 1, tsFindings)
	target, runDir := goTarget(t, []provider{
		{Name: "deadset-go", Command: goFake.command, Languages: []string{"go"}},
		{Name: "deadset-ts", Command: tsFake.command, Languages: []string{"ts"}},
	}, "")
	writeFile(t, filepath.Join(target, "tsconfig.json"), []byte("{}\n"))
	sourceFiles(t, target)
	template := filepath.Join(t.TempDir(), "report.tmpl")
	writeFile(t, template, []byte("{{range .Findings}}{{.Code}} {{.Position.Path}}:{{.Position.Line}}\n{{end}}"))

	got := analyze(t, target, runDir, "--formats=sarif,text,template", "--template="+template)
	if got.code != verdict.Findings {
		t.Fatalf("analyze = %d, want %d\nstderr: %s", got.code, verdict.Findings, got.stderr)
	}
	sarifPath, templatePath := filepath.Join(runDir, "report.json.sarif"), filepath.Join(runDir, "report.json.tmpl")
	lines := strings.Split(strings.TrimSuffix(got.stdout, "\n"), "\n")
	summaryAt := slices.IndexFunc(lines, func(line string) bool { return strings.HasPrefix(line, "summary: ") })
	if summaryAt < 0 || len(lines) < summaryAt+3 ||
		lines[summaryAt+1] != "sarif "+sarifPath || lines[summaryAt+2] != "template "+templatePath {
		t.Errorf("analyze stdout =\n%s\nwant the summary line followed by %q and %q", got.stdout, "sarif "+sarifPath, "template "+templatePath)
	}

	log := readSARIF(t, sarifPath)
	var runs []string
	results := 0
	for _, run := range log.Runs {
		runs = append(runs, run.Tool.Driver.Name)
		results += len(run.Results)
		for _, result := range run.Results {
			if result.PartialFingerprints["primaryLocationLineHash"] == "" {
				t.Errorf("the %s result at %v carries no line fingerprint", result.RuleID, result.Locations)
			}
		}
	}
	if !slices.Equal(runs, []string{"deadset-go", "deadset-ts"}) || results != 3 {
		t.Errorf("the SARIF log holds the runs %q with %d results, want deadset-go and deadset-ts with the 3 findings", runs, results)
	}

	rendered, err := os.ReadFile(templatePath)
	if err != nil {
		t.Fatalf("read %s: %v", templatePath, err)
	}
	want := "DS1002 internal/store/store.go:41\nDS1003 web/src/tabs.ts:31\nDS1001 web/src/wire.ts:8\n"
	if string(rendered) != want {
		t.Errorf("the template rendering is %q, want %q", rendered, want)
	}
}

// A SARIF rendering that cannot read a source file its results name fails the
// run with the failure code, after the merged report is written and before
// anything is presented as the run's result.
func TestAnalyzeFailsASARIFRenderingOfAnUnreadableSourceFile(t *testing.T) {
	t.Parallel()

	f := fakeAnalyzer(t, "deadset-go", []string{"go"}, 1, goFindings)
	target, runDir := goTarget(t, []provider{{Name: "deadset-go", Command: f.command, Languages: []string{"go"}}}, "")
	got := analyze(t, target, runDir, "--formats=sarif")
	if got.code != verdict.Failure || !strings.Contains(got.stderr, "read internal/store/store.go") || got.stdout != "" {
		t.Errorf("analyze = %d, stdout %q, stderr %q, want %d naming the read of internal/store/store.go and no stdout",
			got.code, got.stdout, got.stderr, verdict.Failure)
	}
	if names := listing(t, runDir); !slices.Contains(names, "report.json") || slices.Contains(names, "report.json.sarif") {
		t.Errorf("the run directory holds %q, want the merged report and no SARIF log", names)
	}
}
