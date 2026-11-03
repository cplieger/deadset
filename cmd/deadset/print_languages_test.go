package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	spec "github.com/cplieger/deadset-spec/v8"
	"github.com/cplieger/deadset-spec/v8/document"
	"github.com/cplieger/deadset-spec/v8/vocab"
	"github.com/cplieger/deadset/v2/internal/plan"
	"github.com/cplieger/deadset/v2/internal/source"
)

// printedCensus is the document print-languages prints, as contract/census.schema.json
// spells it.
type printedCensus struct {
	Languages []printedLanguage `json:"languages"`
	Analyzers []printedAnalyzer `json:"analyzers"`
	LeftOut   []printedLeftOut  `json:"left_out"`
}

type printedLanguage struct {
	Language   string `json:"language"`
	Provenance string `json:"provenance"`
}

type printedAnalyzer struct {
	Name      string   `json:"name"`
	Languages []string `json:"languages"`
}

type printedLeftOut struct {
	Language  string   `json:"language"`
	Reason    string   `json:"reason"`
	Consumers []string `json:"consumers"`
}

func printLanguages(t *testing.T, target string, extra ...string) analyzeRun {
	t.Helper()

	var stdout, stderr bytes.Buffer
	args := append([]string{"print-languages", "--target=" + target}, extra...)
	code := (&command{}).dispatch(args, &stdout, &stderr)
	return analyzeRun{stdout: stdout.String(), stderr: stderr.String(), code: code}
}

// decodedCensus holds what print-languages printed to the census schema and
// to the layout the schema states, one indented object ending in a newline,
// and decodes it.
func decodedCensus(t *testing.T, printed string) printedCensus {
	t.Helper()

	schema, err := document.Compile(spec.Contract, "contract/census.schema.json")
	if err != nil {
		t.Fatalf("Setup: compile the census schema: %v", err)
	}
	value, err := document.Decode([]byte(printed))
	if err != nil {
		t.Fatalf("print-languages printed %q, which does not decode: %v", printed, err)
	}
	if found := schema.CheckValue(value); len(found) != 0 {
		t.Errorf("print-languages printed %q, which the census schema refuses: %v", printed, found)
	}
	var compact, indented bytes.Buffer
	if err := json.Compact(&compact, []byte(printed)); err != nil {
		t.Fatalf("compact the census %q: %v", printed, err)
	}
	if err := json.Indent(&indented, compact.Bytes(), "", "  "); err != nil || indented.String()+"\n" != printed {
		t.Errorf("print-languages printed %q, want one object indented by two spaces ending in a newline", printed)
	}
	var got printedCensus
	if err := json.Unmarshal([]byte(printed), &got); err != nil {
		t.Fatalf("decode the census %q: %v", printed, err)
	}
	return got
}

// opened is a command whose analyze records every planned source it is asked
// to open, and opens none, so the run ends before any analyzer runs.
func opened(planned *[]printedAnalyzer) *command {
	return &command{open: func(p *plan.Source, _ string, _ io.Writer) (source.Source, error) {
		languages := make([]string, len(p.Languages))
		for i, l := range p.Languages {
			languages[i] = string(l)
		}
		*planned = append(*planned, printedAnalyzer{Name: p.Entry.Name, Languages: languages})
		return nil, errors.New("not opened")
	}}
}

// print-languages prints the plan analyze makes over the same command line:
// the languages contract/languages.json puts in scope with what put them
// there, the provider entries analyze then opens, in the provider list's
// order, and each language left out with the line analyze prints for it,
// without its opening "deadset: ".
func TestPrintLanguagesNamesWhatAnalyzeRuns(t *testing.T) {
	t.Parallel()

	goEntry := printedAnalyzer{Name: "deadset-go", Languages: []string{"go"}}
	tsEntry := printedAnalyzer{Name: "deadset-ts", Languages: []string{"ts"}}
	for _, c := range []struct {
		name      string
		files     []string
		extra     []string
		consumer  bool
		twice     bool
		want      printedCensus
		wantCodes int
	}{
		{name: "go-module", want: printedCensus{Languages: []printedLanguage{{"go", "detected"}}, Analyzers: []printedAnalyzer{goEntry}}},
		{
			name: "go-module-beside-javascript", files: []string{"web/app.mjs"},
			want: printedCensus{Languages: []printedLanguage{{"go", "detected"}, {"ts", "detected"}}, Analyzers: []printedAnalyzer{goEntry, tsEntry}},
		},
		{
			name: "languages-option", files: []string{"web/app.mjs"}, extra: []string{"--languages=ts"},
			want: printedCensus{Languages: []printedLanguage{{"ts", "flag: --languages"}}, Analyzers: []printedAnalyzer{tsEntry}},
		},
		{
			name: "schema-file-beside-the-module", files: []string{"fixtures/m.proto"},
			want: printedCensus{
				Languages: []printedLanguage{{"go", "detected"}}, Analyzers: []printedAnalyzer{goEntry},
				LeftOut: []printedLeftOut{{Language: "proto", Reason: `this build of deadset holds no reader of "proto", so the run leaves it out of scope`, Consumers: []string{}}},
			},
		},
		{
			name: "schema-consumer", files: []string{"fixtures/m.proto"}, consumer: true,
			want: printedCensus{Languages: []printedLanguage{{"go", "detected"}}, Analyzers: []printedAnalyzer{goEntry}},
		},
		{
			name: "schema-consumer-declared-twice", files: []string{"fixtures/m.proto"}, consumer: true, twice: true,
			want: printedCensus{Languages: []printedLanguage{{"go", "detected"}}, Analyzers: []printedAnalyzer{goEntry}},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			target, runDir := goTarget(t, both, "")
			for _, file := range c.files {
				writeFile(t, filepath.Join(target, filepath.FromSlash(file)), []byte("x\n"))
			}
			extra := c.extra
			want := c.want
			if c.consumer {
				consumer := filepath.Join(filepath.Dir(target), "schema")
				writeFile(t, filepath.Join(consumer, "m.proto"), []byte("syntax = \"proto3\";\n"))
				scopePath := filepath.Join(filepath.Dir(target), "scope.json")
				declared, consumers, named := fmt.Sprintf(`{"path": %q}`, consumer), []string{consumer}, "the consumer "+consumer
				if c.twice {
					// Two entries the scope schema admits as distinct, which
					// resolve to one path.
					declared, consumers, named = `{"path": "schema"}, {"path": "./schema/"}`, []string{consumer, consumer}, "the consumers "+consumer+", "+consumer
				}
				writeFile(t, scopePath, fmt.Appendf(nil, `{"target": {"path": %q}, "consumers": [%s]}`, target, declared))
				extra = append(slices.Clone(extra), "--scope="+scopePath)
				want.LeftOut = []printedLeftOut{{
					Language: "proto", Consumers: consumers,
					Reason: `this build of deadset holds no reader of "proto", so the run leaves it out of scope, and with it ` + named,
				}}
			}

			printed := printLanguages(t, target, extra...)
			if printed.code != vocab.ExitClean || printed.stderr != "" {
				t.Fatalf("print-languages = %d, stderr %q, want %d and nothing on stderr", printed.code, printed.stderr, vocab.ExitClean)
			}
			got := decodedCensus(t, printed.stdout)
			if want.LeftOut == nil {
				want.LeftOut = []printedLeftOut{}
			}
			if !censusEqual(got, want) {
				t.Errorf("print-languages = %+v, want %+v", got, want)
			}

			var planned []printedAnalyzer
			ran := analyzeWith(t, opened(&planned), target, runDir, extra...)
			if !slices.EqualFunc(planned, got.Analyzers, analyzerEqual) {
				t.Errorf("analyze opened %+v, and print-languages named %+v", planned, got.Analyzers)
			}
			for _, left := range got.LeftOut {
				if line := "deadset: " + left.Reason + "\n"; !strings.Contains(ran.stderr, line) {
					t.Errorf("analyze stderr = %q, want the line %q print-languages gave as the reason", ran.stderr, line)
				}
			}
		})
	}
}

func censusEqual(a, b printedCensus) bool {
	return slices.Equal(a.Languages, b.Languages) && slices.EqualFunc(a.Analyzers, b.Analyzers, analyzerEqual) &&
		slices.EqualFunc(a.LeftOut, b.LeftOut, func(x, y printedLeftOut) bool {
			return x.Language == y.Language && x.Reason == y.Reason && slices.Equal(x.Consumers, y.Consumers)
		})
}

func analyzerEqual(a, b printedAnalyzer) bool {
	return a.Name == b.Name && slices.Equal(a.Languages, b.Languages)
}

// The repository configuration's analysis.languages replaces detection, and
// the census names it with the provenance print-config gives it.
func TestPrintLanguagesNamesTheSourceOfAConfiguredLanguage(t *testing.T) {
	t.Parallel()

	target, _ := goTarget(t, both, `, "analysis": {"languages": ["ts"]}`)
	writeFile(t, filepath.Join(target, "a.mjs"), []byte("export const a = 1;\n"))
	printed := printLanguages(t, target)
	if printed.code != vocab.ExitClean {
		t.Fatalf("print-languages = %d, want %d\nstderr: %s", printed.code, vocab.ExitClean, printed.stderr)
	}
	want := []printedLanguage{{"ts", "repository: " + filepath.Join(target, "deadset.json")}}
	if got := decodedCensus(t, printed.stdout); !slices.Equal(got.Languages, want) {
		t.Errorf("print-languages languages = %+v, want %+v", got.Languages, want)
	}
}

// print-languages ends where analyze's plan ends the run, with the same exit
// code and refusal, and prints no census: a census is never empty.
func TestPrintLanguagesEndsAsTheRunEnds(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name      string
		providers []provider
		files     []string
		scope     string
		noModule  bool
		code      int
		names     []string
	}{
		{name: "no-language", noModule: true, files: []string{"node_modules/x/index.js"}, code: vocab.ExitFailure, names: []string{"setup failure: no-program: "}},
		{name: "schema-files-alone", noModule: true, files: []string{"proto/m.proto"}, code: vocab.ExitFailure, names: []string{"setup failure: unsupported-language: "}},
		{name: "language-no-entry-claims", providers: both[:1], files: []string{"a.mjs"}, code: vocab.ExitUsage, names: []string{`claiming "ts", in scope`, "usage: deadset print-languages"}},
		{name: "scope-absent", scope: "absent", code: vocab.ExitUsage, names: []string{"--scope", "no such file", "usage: deadset print-languages"}},
		{
			name: "consumer-no-analyzer-claims", providers: both[:1], scope: `{"target": {"path": "target"}, "consumers": [{"path": "ts-consumer"}]}`,
			code: vocab.ExitUsage, names: []string{"--scope", "ts-consumer holds ts", "no analyzer of the run claims"},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			providers := c.providers
			if providers == nil {
				providers = both
			}
			target, _ := goTarget(t, providers, "")
			if c.noModule {
				if err := os.Remove(filepath.Join(target, "go.mod")); err != nil {
					t.Fatalf("Setup: %v", err)
				}
			}
			for _, file := range c.files {
				writeFile(t, filepath.Join(target, filepath.FromSlash(file)), []byte("x\n"))
			}
			var extra []string
			if c.scope != "" {
				base := filepath.Dir(target)
				writeFile(t, filepath.Join(base, "ts-consumer", "tsconfig.json"), []byte("{}\n"))
				scopePath := filepath.Join(base, "scope.json")
				if c.scope != "absent" {
					writeFile(t, scopePath, []byte(c.scope))
				}
				extra = append(extra, "--scope="+scopePath)
			}
			got := printLanguages(t, target, extra...)
			if got.code != c.code || got.stdout != "" {
				t.Errorf("print-languages(%s) = %d, stdout %q, want %d and no census\nstderr: %s", c.name, got.code, got.stdout, c.code, got.stderr)
			}
			for _, named := range c.names {
				if !strings.Contains(got.stderr, named) {
					t.Errorf("print-languages(%s) stderr = %q, want it to name %s", c.name, got.stderr, named)
				}
			}
			if lines := classLines(got.stderr); c.code == vocab.ExitFailure && len(lines) != 1 {
				t.Errorf("print-languages(%s) stderr holds the class lines %q, want one", c.name, lines)
			}
		})
	}
}
