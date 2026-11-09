package schema

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/cplieger/deadset-spec/v8/vocab"
)

// Settings are the configuration keys the reader reads, the ones
// settings.json lists as read by proto.
type Settings struct {
	// Severity is the severity section: a code's key, else its family's.
	Severity      map[string]vocab.Severity
	MinConfidence vocab.Class
	// RootsSource is the provenance value of roots.patterns, empty where the
	// document names none.
	RootsSource   string
	Disabled      []vocab.Exemption
	Roots         []string
	Plugins       []string
	ImportPaths   []string
	VendoredPaths []string
	Closed        bool
}

// ParseSettings reads the reader's keys from a configuration document, an
// absent key taking its default. Every other member is ignored, so the
// document may hold keys other readers read.
func ParseSettings(data []byte) (Settings, error) {
	var doc struct {
		Severity   map[string]vocab.Severity `json:"severity"`
		Provenance map[string]string         `json:"provenance"`
		Analysis   struct {
			MinConfidence vocab.Class `json:"min_confidence"`
		} `json:"analysis"`
		Exemptions struct {
			Disabled []vocab.Exemption `json:"disabled"`
		} `json:"exemptions"`
		Roots struct {
			Patterns []string `json:"patterns"`
		} `json:"roots"`
		Proto struct {
			Plugins       []string `json:"plugins"`
			ImportPaths   []string `json:"import_paths"`
			VendoredPaths []string `json:"vendored_paths"`
			Closed        bool     `json:"closed"`
		} `json:"proto"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return Settings{}, fmt.Errorf("schema: the configuration document: %w", err)
	}
	s := Settings{
		Severity: doc.Severity, MinConfidence: doc.Analysis.MinConfidence,
		Disabled: doc.Exemptions.Disabled, Roots: doc.Roots.Patterns, Plugins: doc.Proto.Plugins, ImportPaths: doc.Proto.ImportPaths,
		VendoredPaths: doc.Proto.VendoredPaths, Closed: doc.Proto.Closed, RootsSource: doc.Provenance[string(vocab.KeyRootsPatterns)],
	}
	if s.MinConfidence == "" {
		s.MinConfidence = vocab.Probable
	}
	return s, nil
}

// severity is the severity a finding of code carries: the configuration's key
// for the code, then for its family, else the kind's default.
func (s *Settings) severity(code vocab.Code) vocab.Severity {
	for _, key := range []string{string(code), vocab.FamilyOf(code)} {
		if set, named := s.Severity[key]; named {
			return set
		}
	}
	kind, _ := vocab.KindOf(code)
	return kind.DefaultSeverity
}

func (s *Settings) disabled(class vocab.Exemption) bool { return slices.Contains(s.Disabled, class) }

// below reports whether class lies below analysis.min_confidence.
func (s *Settings) below(class vocab.Class) bool {
	classes := vocab.Classes()
	return slices.Index(classes, class) > slices.Index(classes, s.MinConfidence)
}
