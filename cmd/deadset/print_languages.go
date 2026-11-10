package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"

	"github.com/cplieger/deadset-spec/v8/vocab"
	"github.com/cplieger/deadset/v2/internal/config"
	"github.com/cplieger/deadset/v2/internal/failure"
	"github.com/cplieger/deadset/v2/internal/plan"
	"github.com/cplieger/deadset/v2/internal/run"
)

// detected is the provenance of a language the census of the target put in
// scope.
const detected = "detected"

// bindPrintLanguages binds the print-languages verb, which prints the census
// of the plan analyze makes over the same command line, before any analyzer
// runs, and ends where that plan ends the run.
func (*command) bindPrintLanguages(set *flag.FlagSet) runner {
	target := set.String("target", ".", "")
	scopePath := set.String(scopeOption, "", "")
	central := set.String("central", "", "")
	return func(ctx context.Context, stdout, _ io.Writer) (int, error) {
		resolved, root, err := resolveConfiguration(ctx, set, *target, *central)
		if err != nil {
			return 0, err
		}
		declared, err := readScope(ctx, *scopePath, root)
		if err != nil {
			return 0, err
		}
		planned, err := run.Plan(resolved, declared)
		if err != nil {
			return 0, routed(err)
		}
		document, err := censusOf(resolved, planned).encode()
		if err != nil {
			return 0, err
		}
		if _, err := stdout.Write(document); err != nil {
			return 0, failure.Wrap(vocab.RunUnwrittenOutput, fmt.Errorf("write the census: %w", err))
		}
		return vocab.ExitClean, nil
	}
}

// census is an instance of contract/census.schema.json, its members in the
// schema's order.
type census struct {
	Languages []censusLanguage `json:"languages"`
	Analyzers []censusAnalyzer `json:"analyzers"`

	// LeftOut is always empty: census.schema.json leaves out only a language
	// whose reader the orchestrator lacks, and deadset links every reader.
	LeftOut []struct{} `json:"left_out"`
}

type censusLanguage struct {
	Language   vocab.Language `json:"language"`
	Provenance string         `json:"provenance"`
}

type censusAnalyzer struct {
	Name      string           `json:"name"`
	Languages []vocab.Language `json:"languages"`
}

func censusOf(resolved *config.Resolved, planned *plan.Plan) *census {
	provenance := detected
	if !planned.Detected {
		provenance = resolved.Provenance(vocab.KeyAnalysisLanguages)
	}
	c := &census{Languages: []censusLanguage{}, Analyzers: []censusAnalyzer{}, LeftOut: []struct{}{}}
	for _, language := range planned.Languages {
		c.Languages = append(c.Languages, censusLanguage{Language: language, Provenance: provenance})
	}
	for i := range planned.Sources {
		if !planned.Sources[i].InProcess {
			c.Analyzers = append(c.Analyzers, censusAnalyzer{Name: planned.Sources[i].Entry.Name, Languages: planned.Sources[i].Languages})
		}
	}
	return c
}

// encode is the census as one object indented by two spaces, ending in a
// newline, a path in it written as it is.
func (c *census) encode() ([]byte, error) {
	var document bytes.Buffer
	encoder := json.NewEncoder(&document)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(c); err != nil {
		return nil, fmt.Errorf("encode the census: %w", err)
	}
	return document.Bytes(), nil
}
