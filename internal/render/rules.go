package render

import (
	"bytes"
	_ "embed" // the issue-kind vocabulary is embedded
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/cplieger/deadset/internal/report"
)

// vocabulary is the Contract's issue-kind vocabulary, a copy a test pins byte
// for byte to the version this module implements.
//
//go:embed kinds.json
var vocabulary []byte

// kind is one live issue kind, holding the members a SARIF rule is built from.
type kind struct {
	Code            string          `json:"code"`
	Name            string          `json:"name"`
	Rule            string          `json:"rule"`
	Precondition    string          `json:"precondition"`
	DefaultSeverity report.Severity `json:"default_severity"`
	MaxClass        report.Class    `json:"max_class"`
	Languages       []string        `json:"languages"`
}

// liveKinds is every live kind of the vocabulary, ordered by code.
var liveKinds = sync.OnceValues(func() ([]kind, error) {
	var held struct {
		Kinds []kind `json:"kinds"`
	}
	if err := json.NewDecoder(bytes.NewReader(vocabulary)).Decode(&held); err != nil {
		return nil, fmt.Errorf("render: read the issue-kind vocabulary: %w", err)
	}
	slices.SortFunc(held.Kinds, func(a, b kind) int { return strings.Compare(a.Code, b.Code) })
	return held.Kinds, nil
})

// rulesFor is one rule per live kind that applies to any of languages, ordered
// by code, whether or not a result names it: a fixed list keeps every rule's
// index stable across runs and configurations. Nil languages selects every
// live kind.
func rulesFor(languages []string) ([]sarifRule, error) {
	kinds, err := liveKinds()
	if err != nil {
		return nil, err
	}
	rules := make([]sarifRule, 0, len(kinds))
	for i := range kinds {
		k := &kinds[i]
		if languages != nil && !slices.ContainsFunc(languages, func(l string) bool { return slices.Contains(k.Languages, l) }) {
			continue
		}
		help := k.Rule
		if k.Precondition != "" {
			help += "\n\n" + k.Precondition
		}
		rules = append(rules, sarifRule{
			ID:                   k.Code,
			Name:                 k.Name,
			ShortDescription:     sarifMessage{Text: firstSentence(k.Rule)},
			FullDescription:      sarifMessage{Text: k.Rule},
			Help:                 sarifMessage{Text: help},
			DefaultConfiguration: sarifConfiguration{Level: levelOf(k.DefaultSeverity)},
			Properties: sarifRuleProperties{
				Precision: precisionOf(k.MaxClass),
				Problem:   sarifProblem{Severity: problemOf(k.DefaultSeverity)},
			},
		})
	}
	return rules, nil
}

// firstSentence is text up to and including the first full stop that a space
// follows or that ends text, and all of text where none does.
func firstSentence(text string) string {
	for i := range len(text) {
		if text[i] == '.' && (i+1 == len(text) || text[i+1] == ' ') {
			return text[:i+1]
		}
	}
	return text
}

// The levels a severity maps to, and the problem severity and the precisions
// only a rule carries.
const (
	levelError   = "error"
	levelWarning = "warning"
	levelNote    = "note"

	problemRecommendation = "recommendation"

	precisionVeryHigh = "very-high"
	precisionHigh     = "high"
	precisionMedium   = "medium"
)

// levelOf is the level a severity maps to on a result and on a rule's default
// configuration.
func levelOf(severity report.Severity) string {
	switch severity {
	case report.SeverityDeny:
		return levelError
	case report.SeverityWarn:
		return levelWarning
	default:
		return levelNote
	}
}

// problemOf is the problem severity a kind's default severity maps to.
func problemOf(severity report.Severity) string {
	switch severity {
	case report.SeverityDeny:
		return levelError
	case report.SeverityWarn:
		return levelWarning
	default:
		return problemRecommendation
	}
}

// precisionOf is the precision a kind's confidence ceiling maps to.
func precisionOf(ceiling report.Class) string {
	switch ceiling {
	case report.ClassCertain:
		return precisionVeryHigh
	case report.ClassProbable:
		return precisionHigh
	default:
		return precisionMedium
	}
}
