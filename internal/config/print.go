package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"slices"
)

// Print writes the resolved configuration, every key in the closed key list's order
// and the severity codes in ascending order, followed by the provenance object that
// names where each setting came from, as one indented JSON object ending in a
// newline. The output is an instance of the closed key list, and read back as a
// repository configuration it resolves to the same configuration: resolution
// ignores the provenance object.
func (r *Resolved) Print(w io.Writer) error {
	document, err := r.render(func(*node) bool { return true })
	if err != nil {
		return err
	}
	if _, err := w.Write(document); err != nil {
		return fmt.Errorf("write the resolved configuration: %w", err)
	}
	return nil
}

// Split returns the configuration document an analyzer claiming languages receives,
// in the form Print writes. It holds every key the Contract gives every product or
// every analyzer, and the section of each language named, and leaves out the keys
// the orchestrator alone reads and the section of every other language; its
// provenance object names the source of each setting it holds. The build matrix is
// passed whole: an analysis reads the entries of the shape its language is built
// from and leaves the others to the analysis that reads them.
func (r *Resolved) Split(languages ...string) ([]byte, error) {
	return r.render(func(key *node) bool {
		switch key.owner {
		case orchestrator:
			return false
		case languageAnalyzer:
			// A language's section goes to the analyzers of the languages named,
			// and the keys inside it go with it.
			return key.kind != section || slices.Contains(languages, key.name)
		case everyProduct, everyAnalyzer:
		}
		return true
	})
}

// member is one member of an object written in a fixed order. A value is an
// object, a JSON document, or a string.
type member struct {
	value any
	name  string
}

// render writes the resolved configuration as one indented JSON object, holding the
// keys include admits and the provenance of each setting among them.
func (r *Resolved) render(include func(*node) bool) ([]byte, error) {
	var paths []string
	document := r.object(&r.keys, "", include, &paths)
	provenance := make([]member, len(paths))
	for i, path := range paths {
		provenance[i] = member{name: path, value: r.provenance[path]}
	}
	document = append(document, member{name: provenanceSection, value: provenance})

	compact, err := appendValue(nil, document)
	if err != nil {
		return nil, fmt.Errorf("render the resolved configuration: %w", err)
	}
	var indented bytes.Buffer
	if err := json.Indent(&indented, compact, "", "  "); err != nil {
		return nil, fmt.Errorf("render the resolved configuration: %w", err)
	}
	indented.WriteByte('\n')
	return indented.Bytes(), nil
}

// object returns the members of the section n at the dotted path at that include
// admits, and appends the path of each setting among them to paths.
func (r *Resolved) object(n *node, at string, include func(*node) bool, paths *[]string) []member {
	members := []member{}
	for i := range n.children {
		key := &n.children[i]
		if !include(key) {
			continue
		}
		path := joinKey(at, key.name)
		switch key.kind {
		case section:
			members = append(members, member{name: key.name, value: r.object(key, path, include, paths)})
		case setting, passThrough:
			members = append(members, member{name: key.name, value: r.values[path]})
			*paths = append(*paths, path)
		case codes:
			members = append(members, member{name: key.name, value: r.codes(paths)})
		case annotations:
		}
	}
	return members
}

// codes returns the resolved severity object, its codes in ascending order, and
// appends the path of each code to paths, or the object's own path when it is
// empty.
func (r *Resolved) codes(paths *[]string) []member {
	codes := sortedKeys(r.severity)
	if len(codes) == 0 {
		*paths = append(*paths, severitySection)
	}
	members := make([]member, len(codes))
	for i, code := range codes {
		members[i] = member{name: code, value: r.severity[code]}
		*paths = append(*paths, joinKey(severitySection, code))
	}
	return members
}

// appendValue appends one value, compact, to b: an object as its members in order,
// a JSON document as written, a string quoted.
func appendValue(b []byte, value any) ([]byte, error) {
	switch v := value.(type) {
	case []member:
		return appendObject(b, v)
	case json.RawMessage:
		var compact bytes.Buffer
		if err := json.Compact(&compact, v); err != nil {
			return nil, err
		}
		return append(b, compact.Bytes()...), nil
	case string:
		var quoted bytes.Buffer
		enc := json.NewEncoder(&quoted)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(v); err != nil {
			return nil, err
		}
		return append(b, bytes.TrimSuffix(quoted.Bytes(), []byte("\n"))...), nil
	default:
		return nil, fmt.Errorf("no JSON form for %T", value)
	}
}

// appendObject appends an object's members to b in their order.
func appendObject(b []byte, members []member) ([]byte, error) {
	b = append(b, '{')
	for i, m := range members {
		if i > 0 {
			b = append(b, ',')
		}
		var err error
		if b, err = appendValue(b, m.name); err != nil {
			return nil, err
		}
		b = append(b, ':')
		if b, err = appendValue(b, m.value); err != nil {
			return nil, err
		}
	}
	return append(b, '}'), nil
}
