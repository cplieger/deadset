package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"

	"github.com/cplieger/deadset-spec/v8/vocab"
)

// Inputs are what one resolution reads.
type Inputs struct {
	// Flags is the settings the command line supplied, as [FlagSettings]
	// returns them, by key.
	Flags      map[vocab.Key]json.RawMessage
	Repository Document
	Central    Document
}

// Resolved is a resolved configuration: every setting of the closed key list,
// each with the source that supplied it.
type Resolved struct {
	values     map[vocab.Key]json.RawMessage
	keyed      map[vocab.Key]map[string]json.RawMessage
	provenance map[string]string
	Config     config
}

// The kinds of source, as a provenance value spells them, from the highest rank to
// the lowest.
const (
	fromFlag       = "flag"
	fromRepository = "repository"
	fromCentral    = "central"
)

// source is one configuration source a resolution reads: the values it
// supplies by key, its kind, and the file it was read from, which the flags
// have none of.
type source struct {
	values map[vocab.Key]json.RawMessage
	kind   string
	path   string
}

// origin is the provenance value a setting at or below key carries when this
// source supplies it: the kind, a colon, a space and the file, or the flag
// that supplies key.
func (s *source) origin(key vocab.Key) string {
	return s.kind + ": " + s.label(key)
}

// label names this source in a refusal of the value at or below key.
func (s *source) label(key vocab.Key) string {
	if s.kind == fromFlag {
		row, _ := vocab.ConfigKeyOf(key)
		return "--" + row.Option
	}
	return s.path
}

// Resolve applies the command-line flags over the repository configuration over
// the central configuration over each key's default, and records the source of
// every resolved setting. It returns an [*Error] for a document the
// configuration schema refuses, for a flag supplying no setting an option
// names, and for a resolution that leaves a setting with no default unset,
// which for the target kind is every resolution no source supplies one for:
// the target kind is never inferred.
func Resolve(in *Inputs) (*Resolved, error) {
	sources, err := readSources(in)
	if err != nil {
		return nil, err
	}
	r := &Resolved{
		values:     map[vocab.Key]json.RawMessage{},
		keyed:      map[vocab.Key]map[string]json.RawMessage{},
		provenance: map[string]string{},
	}
	keys := vocab.ConfigKeys()
	for i := range keys {
		key := &keys[i]
		switch key.Role {
		case vocab.RoleSetting:
			if !r.resolve(key, sources) {
				return nil, missing(key.Key, in)
			}
		case vocab.RoleKeyed:
			r.resolveKeyed(key.Key, sources)
		case vocab.RoleSection, vocab.RoleIgnored:
		}
	}
	if r.Config, err = readConfig(r.values); err != nil {
		return nil, fmt.Errorf("read the resolved configuration: %w", err)
	}
	return r, nil
}

// readSources reads and checks the sources one resolution carries, in the
// order they outrank each other, skipping each it does not carry. A flag is
// checked as the one-setting document it supplies.
func readSources(in *Inputs) ([]source, error) {
	var sources []source
	if len(in.Flags) > 0 {
		flags := source{values: in.Flags, kind: fromFlag}
		for _, key := range slices.Sorted(maps.Keys(in.Flags)) {
			if row, _ := vocab.ConfigKeyOf(key); row.Option == "" {
				return nil, unimplemented("the command line", string(key), "no flag supplies it")
			}
			if err := check(nested(key, in.Flags[key]), flags.label); err != nil {
				return nil, err
			}
		}
		sources = append(sources, flags)
	}
	for _, held := range []struct {
		kind     string
		document Document
	}{{fromRepository, in.Repository}, {fromCentral, in.Central}} {
		if held.document.Data == nil {
			continue
		}
		s := source{kind: held.kind, path: held.document.Path}
		if err := check(held.document.Data, s.label); err != nil {
			return nil, err
		}
		s.values = suppliedBy(held.document.Data)
		sources = append(sources, s)
	}
	return sources, nil
}

// nested is the document holding one value at its key's dotted path.
func nested(key vocab.Key, value json.RawMessage) []byte {
	names := strings.Split(string(key), ".")
	document := value
	for _, held := range slices.Backward(names) {
		name, _ := json.Marshal(held) // a string always encodes
		document = slices.Concat([]byte("{"), name, []byte(":"), document, []byte("}"))
	}
	return document
}

// resolve assigns one setting the value of the highest-ranked source carrying
// one, or its default, and records where it came from. It reports false for a
// setting with no default that no source supplies.
func (r *Resolved) resolve(key *vocab.ConfigKey, sources []source) bool {
	for i := range sources {
		if value, held := sources[i].values[key.Key]; held {
			r.values[key.Key] = value
			r.provenance[string(key.Key)] = sources[i].origin(key.Key)
			return true
		}
	}
	if key.Default == "" {
		return false
	}
	r.values[key.Key] = json.RawMessage(key.Default)
	r.provenance[string(key.Key)] = "default"
	return true
}

// resolveKeyed resolves a keyed section one member at a time, so a member only a
// lower-ranked source names keeps that source's value. A section that resolves
// empty carries one provenance entry for itself, naming the highest-ranked source
// that wrote one.
func (r *Resolved) resolveKeyed(key vocab.Key, sources []source) {
	resolved := map[string]json.RawMessage{}
	r.provenance[string(key)] = "default"
	for i := len(sources) - 1; i >= 0; i-- {
		value, held := sources[i].values[key]
		if !held {
			continue
		}
		r.provenance[string(key)] = sources[i].origin(key)
		var members map[string]json.RawMessage
		_ = json.Unmarshal(value, &members) // the schema admitted an object
		for name, set := range members {
			resolved[name] = set
			r.provenance[joinKey(string(key), name)] = sources[i].origin(key)
		}
	}
	if len(resolved) > 0 {
		delete(r.provenance, string(key))
	}
	r.keyed[key] = resolved
}

// missing refuses a resolution that leaves a setting with no default unset, naming
// the setting and what each source said about it.
func missing(key vocab.Key, in *Inputs) *Error {
	return &Error{
		Message: fmt.Sprintf("%s is not set, and it has no default and is never inferred: "+
			"no flag supplies it, %s, and %s", key, searched(fromRepository, in.Repository), searched(fromCentral, in.Central)),
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
func readConfig(values map[vocab.Key]json.RawMessage) (config, error) {
	var c config
	var sort string
	var maxFindings int64
	providers, providersErr := readProviders(values[vocab.KeyProvidersAnalyzers])
	c.Providers = providers
	err := errors.Join(
		providersErr,
		json.Unmarshal(values[vocab.KeyAnalysisLanguages], &c.Languages),
		json.Unmarshal(values[vocab.KeyReportersFormats], &c.Reporters.Formats),
		json.Unmarshal(values[vocab.KeyReportersSort], &sort),
		json.Unmarshal(values[vocab.KeyReportersMaxFindings], &maxFindings),
		json.Unmarshal(values[vocab.KeyReportersFailOn], &c.Reporters.FailOn),
	)
	c.Reporters.BySize = sort == sortBySize
	// The schema's maximum fits int64 but not a 32-bit int, where a cap above
	// math.MaxInt can never bind a findings list.
	c.Reporters.MaxFindings = int(min(maxFindings, int64(math.MaxInt)))
	return c, err
}
