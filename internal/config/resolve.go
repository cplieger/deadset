package config

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Inputs are what one resolution reads.
type Inputs struct {
	// ContractVersion is the Contract version this product implements, which
	// the resolved configuration names when no source names one.
	ContractVersion string
	Repository      Document
	Central         Document
	// Flags is the settings the command line supplied, as [FlagSettings] returns
	// them: one member per setting, named by its dotted path. It is nil when the
	// command line supplied none.
	Flags []byte
}

// Resolved is a resolved configuration: every setting the closed key list declares,
// each with the source that supplied it.
type Resolved struct {
	values     map[string]json.RawMessage
	severity   map[string]json.RawMessage
	provenance map[string]string
	Config     Config
	keys       node
}

// The kinds of source, as a provenance value spells them, from the highest rank to
// the lowest.
const (
	fromFlag       = "flag"
	fromRepository = "repository"
	fromCentral    = "central"
)

// source is one configuration source a resolution reads: the values it supplies,
// its kind, and the file it was read from, which the flags have none of.
type source struct {
	values supplied
	kind   string
	path   string
}

// origin is the provenance value the setting at path carries when this source
// supplies it: the kind, a colon, a space and the file, or the flag that carries the
// setting.
func (s *source) origin(path string) string {
	return s.kind + ": " + s.label(path)
}

// label names this source in a refusal of the setting at path.
func (s *source) label(path string) string {
	if s.kind == fromFlag {
		f, _ := flagFor(path)
		return "--" + f.name
	}
	return s.path
}

// Resolve applies the command-line flags over the repository configuration over
// the central configuration over each key's documented default, and records the
// source of every resolved setting. It returns an [*Error] for a document that is
// not one instance of the closed key list, for a key the list does not declare, and
// for a resolution that leaves a setting with no default unset, which for the target
// kind is every resolution no source supplies one for: the target kind is never
// inferred.
func Resolve(in *Inputs) (*Resolved, error) {
	keys := keyList(in.ContractVersion)
	sources, err := readSources(&keys, in)
	if err != nil {
		return nil, err
	}
	for i := range sources {
		if refused := checkSource(&keys, "", &sources[i]); refused != nil {
			return nil, refused
		}
	}
	r := &Resolved{
		keys:       keys,
		values:     make(map[string]json.RawMessage),
		severity:   make(map[string]json.RawMessage),
		provenance: make(map[string]string),
	}
	if path, unset := r.resolve(&keys, "", sources); unset {
		return nil, missing(path, in)
	}
	r.resolveSeverity(sources)
	if r.Config, err = readConfig(r.values); err != nil {
		return nil, fmt.Errorf("read the resolved configuration: %w", err)
	}
	return r, nil
}

// readSources reads the sources one resolution carries, in the order they outrank
// each other, skipping each it does not carry.
func readSources(keys *node, in *Inputs) ([]source, error) {
	var sources []source
	if in.Flags != nil {
		values, err := readFlags(in.Flags, "the command line")
		if err != nil {
			return nil, err
		}
		sources = append(sources, source{values: values, kind: fromFlag})
	}
	for _, held := range []struct {
		kind     string
		document Document
	}{{fromRepository, in.Repository}, {fromCentral, in.Central}} {
		if held.document.Data == nil {
			continue
		}
		values, err := readDocument(keys, held.document.Data, held.document.Path)
		if err != nil {
			return nil, err
		}
		sources = append(sources, source{values: values, kind: held.kind, path: held.document.Path})
	}
	return sources, nil
}

// checkSource checks every value one source supplies below n, in the key list's
// order, so the first refusal is the same on every run.
func checkSource(n *node, at string, s *source) error {
	for i := range n.children {
		key := &n.children[i]
		path := joinKey(at, key.name)
		if key.kind == section {
			if err := checkSource(key, path, s); err != nil {
				return err
			}
			continue
		}
		value, held := s.values[path]
		if !held || key.check == nil {
			continue
		}
		if rejected := key.check(value); rejected != nil {
			return refusal(s.label(path), path, rejected)
		}
	}
	return nil
}

// refusal names a value one setting rejected, at the place inside it the rejection
// names.
func refusal(label, path string, rejected *rejection) *Error {
	if rejected.undeclared {
		return unimplemented(label, path+rejected.at, rejected.detail)
	}
	return malformed(label, path+rejected.at, "%s", rejected.detail)
}

// resolve assigns every setting below n the value of the highest-ranked source
// carrying one, or its default, and records where it came from. It returns the
// first setting with no default that no source supplies.
func (r *Resolved) resolve(n *node, at string, sources []source) (string, bool) {
	for i := range n.children {
		key := &n.children[i]
		path := joinKey(at, key.name)
		switch key.kind {
		case section:
			if unsetPath, unset := r.resolve(key, path, sources); unset {
				return unsetPath, true
			}
			continue
		case codes, annotations:
			continue
		case setting, passThrough:
		}
		r.provenance[path] = "default"
		r.values[path] = key.fallback
		for _, s := range sources {
			if value, held := s.values[path]; held {
				r.values[path] = value
				r.provenance[path] = s.origin(path)
				break
			}
		}
		if r.values[path] == nil {
			return path, true
		}
	}
	return "", false
}

// resolveSeverity resolves the severity object one code at a time, so a code only a
// lower-ranked source names keeps that source's value. An object that resolves empty
// carries one provenance entry for itself, naming the highest-ranked source that
// wrote one.
func (r *Resolved) resolveSeverity(sources []source) {
	r.provenance[severitySection] = "default"
	for i := len(sources) - 1; i >= 0; i-- {
		s := &sources[i]
		value, held := s.values[severitySection]
		if !held {
			continue
		}
		r.provenance[severitySection] = s.origin(severitySection)
		var keys map[string]json.RawMessage
		if !decoded(value, &keys) {
			continue
		}
		for key, severity := range keys {
			path := joinKey(severitySection, key)
			r.severity[key] = severity
			r.provenance[path] = s.origin(path)
		}
	}
	if len(r.severity) > 0 {
		delete(r.provenance, severitySection)
	}
}

// missing refuses a resolution that leaves a setting with no default unset, naming
// the setting and what each source said about it.
func missing(path string, in *Inputs) *Error {
	return &Error{
		Key: path,
		Message: fmt.Sprintf("%s is not set, and it has no default and is never inferred: "+
			"no flag supplies it, %s, and %s", path, searched(fromRepository, in.Repository), searched(fromCentral, in.Central)),
	}
}

// searched says what one configuration document a resolution searched held.
func searched(kind string, document Document) string {
	switch {
	case document.Path == "":
		return "no " + kind + " configuration is named"
	case document.Data == nil:
		return "there is no " + kind + " configuration at " + document.Path
	default:
		return "the " + kind + " configuration " + document.Path + " does not set it"
	}
}

// readConfig reads the settings the orchestrator reads out of the resolved values.
func readConfig(values map[string]json.RawMessage) (Config, error) {
	var c Config
	providers, providersErr := readProviders(values[providerListKey])
	c.Providers = providers
	err := errors.Join(
		providersErr,
		json.Unmarshal(values["analysis.languages"], &c.Languages),
		json.Unmarshal(values["reporters.formats"], &c.Reporters.Formats),
		json.Unmarshal(values["reporters.sort"], &c.Reporters.Sort),
		json.Unmarshal(values["reporters.cascade"], &c.Reporters.Cascade),
		json.Unmarshal(values["reporters.max_findings"], &c.Reporters.MaxFindings),
		json.Unmarshal(values["reporters.fail_on"], &c.Reporters.FailOn),
	)
	return c, err
}
