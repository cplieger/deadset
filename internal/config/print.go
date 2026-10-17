package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/cplieger/deadset-spec/v8/vocab"
	"github.com/cplieger/deadset/v2/internal/failure"
)

// node is one key of the closed key list with the keys below it, in the
// table's order.
type node struct {
	name     string
	children []*node
	key      vocab.ConfigKey
}

// keyTree is the closed key list as the document nests it.
var keyTree = sync.OnceValue(func() *node {
	root := &node{}
	held := map[vocab.Key]*node{"": root}
	for _, key := range vocab.ConfigKeys() {
		at, name := "", string(key.Key)
		if cut := strings.LastIndexByte(name, '.'); cut >= 0 {
			at, name = name[:cut], name[cut+1:]
		}
		child := &node{key: key, name: name}
		parent := held[vocab.Key(at)]
		parent.children = append(parent.children, child)
		held[key.Key] = child
	}
	return root
})

// Print writes the resolved configuration, every key in the closed key list's
// order and the members of a keyed section in ascending order, followed by the
// provenance object that names where each setting came from, as one indented
// JSON object ending in a newline. The output is an instance of the
// configuration schema, and read back as a repository configuration it
// resolves to the same configuration: resolution ignores the provenance
// object.
func (r *Resolved) Print(w io.Writer) error {
	document, err := r.render(func(*vocab.ConfigKey) bool { return true })
	if err != nil {
		return err
	}
	if _, err := w.Write(document); err != nil {
		return failure.Wrap(vocab.RunUnwrittenOutput, fmt.Errorf("write the resolved configuration: %w", err))
	}
	return nil
}

// Split returns the configuration document an analyzer claiming languages
// receives, in the form Print writes: every key the setting table delivers to
// one of the languages, and the provenance of each setting it holds.
func (r *Resolved) Split(languages ...vocab.Language) ([]byte, error) {
	return r.render(func(key *vocab.ConfigKey) bool {
		return slices.ContainsFunc(languages, key.DeliveredTo)
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
func (r *Resolved) render(include func(*vocab.ConfigKey) bool) ([]byte, error) {
	var paths []string
	document := r.object(keyTree(), include, &paths)
	provenance := make([]member, len(paths))
	for i, path := range paths {
		provenance[i] = member{name: path, value: r.provenance[path]}
	}
	if key, _ := vocab.ConfigKeyOf(vocab.KeyProvenance); include(&key) {
		document = append(document, member{name: string(vocab.KeyProvenance), value: provenance})
	}

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

// object returns the members below n that include admits, and appends the
// path of each setting among them to paths.
func (r *Resolved) object(n *node, include func(*vocab.ConfigKey) bool, paths *[]string) []member {
	members := []member{}
	for _, child := range n.children {
		key := &child.key
		if !include(key) {
			continue
		}
		switch key.Role {
		case vocab.RoleSection:
			members = append(members, member{name: child.name, value: r.object(child, include, paths)})
		case vocab.RoleSetting:
			members = append(members, member{name: child.name, value: r.values[key.Key]})
			*paths = append(*paths, string(key.Key))
		case vocab.RoleKeyed:
			members = append(members, member{name: child.name, value: r.keyedMembers(key.Key, paths)})
		case vocab.RoleIgnored:
		}
	}
	return members
}

// keyedMembers returns a resolved keyed section, its members in ascending
// order, and appends the path of each member to paths, or the section's own
// path when it is empty.
func (r *Resolved) keyedMembers(key vocab.Key, paths *[]string) []member {
	names := slices.Sorted(maps.Keys(r.keyed[key]))
	if len(names) == 0 {
		*paths = append(*paths, string(key))
	}
	members := make([]member, len(names))
	for i, name := range names {
		members[i] = member{name: name, value: r.keyed[key][name]}
		*paths = append(*paths, joinKey(string(key), name))
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
