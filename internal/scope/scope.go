// Package scope reads the scope document an invocation names: the target a run
// reports on and the consumers whose references count against it, each a
// directory on the local filesystem. It is the Contract's scope document, the
// one every analyzer's analyze verb also reads.
package scope

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// ErrDocument reports a scope document the Contract's scope schema refuses.
var ErrDocument = errors.New("scope: the document is refused")

// The roles a document gives a module, each the one its position admits.
const (
	roleTarget   = "target"
	roleConsumer = "consumer"
)

// Module is one module a scope document names.
type Module struct {
	// ID is the name the module publishes itself under, empty where the
	// document leaves the name to the analyzer's load.
	ID string

	// Path is the module's directory, absolute and clean.
	Path string
}

// Document is one scope document as read: the target, the workspace file and
// the consumers in the order the document lists them.
type Document struct {
	// Workspace is the absolute, clean path of the workspace file the document
	// names, and empty where it names none.
	Workspace string

	Target    Module
	Consumers []Module
}

// Read reads the scope document at path, resolving a relative path inside it
// against the directory that holds it. Every document the Contract's scope
// schema refuses (an undeclared member compared as bytes, a member named
// twice, an absent target or path, a null, an empty string, a role other than
// the module's place) is an error satisfying errors.Is(err, [ErrDocument])
// that names the JSON Pointer at fault; an unreadable document wraps the
// read's error, so an absent one satisfies errors.Is(err, fs.ErrNotExist).
func Read(path string) (*Document, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("scope: %w", err)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("scope: %w", err)
	}
	r := reader{file: path, base: filepath.Dir(absolute)}
	return r.document(data)
}

// Root is the deepest directory that holds the target, every consumer and the
// workspace file, which is the one directory every path of the document is
// beneath.
func (d *Document) Root() string {
	root := d.Target.Path
	for _, consumer := range d.Consumers {
		root = ancestor(root, consumer.Path)
	}
	if d.Workspace != "" {
		root = ancestor(root, filepath.Dir(d.Workspace))
	}
	return root
}

// ancestor is the deepest directory holding both of two absolute, clean
// paths: dir itself or one of its parents.
func ancestor(dir, path string) string {
	for {
		if relative, err := filepath.Rel(dir, path); err == nil && filepath.IsLocal(relative) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return dir
		}
		dir = parent
	}
}

// reader reads one document: file is the path it was named by, which every
// refusal names, and base the absolute directory holding it, which a relative
// path inside it is resolved against.
type reader struct {
	file, base string
}

// document reads the document's one object.
func (r *reader) document(data []byte) (*Document, error) {
	members, err := r.object(data, "", []string{"target"}, []string{"workspace", "consumers"})
	if err != nil {
		return nil, err
	}
	target, err := r.module(members["target"], "/target", roleTarget)
	if err != nil {
		return nil, err
	}
	read := &Document{Target: target}
	if raw, stated := members["workspace"]; stated {
		workspace, err := r.text(raw, "/workspace")
		if err != nil {
			return nil, err
		}
		read.Workspace = r.resolve(workspace)
	}
	if raw, stated := members["consumers"]; stated {
		var elements []json.RawMessage
		if bytes.Equal(raw, []byte("null")) || json.Unmarshal(raw, &elements) != nil {
			return nil, r.refusal("/consumers", "want an array")
		}
		for i, element := range elements {
			consumer, err := r.module(element, "/consumers/"+strconv.Itoa(i), roleConsumer)
			if err != nil {
				return nil, err
			}
			read.Consumers = append(read.Consumers, consumer)
		}
	}
	return read, nil
}

// module reads the module object raw holds, at the pointer at, whose position
// gives it role.
func (r *reader) module(raw json.RawMessage, at, role string) (Module, error) {
	members, err := r.object(raw, at, []string{"path"}, []string{"id", "role"})
	if err != nil {
		return Module{}, err
	}
	path, err := r.text(members["path"], at+"/path")
	if err != nil {
		return Module{}, err
	}
	read := Module{Path: r.resolve(path)}
	if raw, stated := members["id"]; stated {
		if read.ID, err = r.text(raw, at+"/id"); err != nil {
			return Module{}, err
		}
	}
	if raw, stated := members["role"]; stated {
		declared, err := r.text(raw, at+"/role")
		if err != nil {
			return Module{}, err
		}
		if declared != role {
			return Module{}, r.refusal(at+"/role", fmt.Sprintf("%q is not %q, the role of the module's place in the document", declared, role))
		}
	}
	return read, nil
}

// object reads the one JSON object data holds as the raw value of each
// member, refusing a member neither required nor optional declares, a member
// named twice, a required member that is absent, and anything after the
// object. at is the object's JSON Pointer.
func (r *reader) object(data []byte, at string, required, optional []string) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if open, err := decoder.Token(); err != nil || open != json.Delim('{') {
		return nil, r.refusal(at, "want an object")
	}
	members := map[string]json.RawMessage{}
	declared := slices.Concat(required, optional)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, r.refusal(at, err.Error())
		}
		name, _ := token.(string)
		member := at + "/" + strings.NewReplacer("~", "~0", "/", "~1").Replace(name)
		switch _, seen := members[name]; {
		case !slices.Contains(declared, name):
			return nil, r.refusal(member, "the scope schema declares no such member")
		case seen:
			return nil, r.refusal(member, "the member is named twice")
		}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return nil, r.refusal(member, err.Error())
		}
		members[name] = raw
	}
	if _, err := decoder.Token(); err != nil {
		return nil, r.refusal(at, "want the object closed: "+err.Error())
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, r.refusal("", "want one JSON object and nothing after it")
	}
	for _, name := range required {
		if _, stated := members[name]; !stated {
			return nil, r.refusal(at+"/"+name, "a required member is absent")
		}
	}
	return members, nil
}

// text reads the non-empty string raw holds, at the pointer at.
func (r *reader) text(raw json.RawMessage, at string) (string, error) {
	var read string
	if bytes.Equal(raw, []byte("null")) || json.Unmarshal(raw, &read) != nil {
		return "", r.refusal(at, "want a string")
	}
	if read == "" {
		return "", r.refusal(at, "want a non-empty string")
	}
	return read, nil
}

// resolve is the absolute, clean path a path inside the document names.
func (r *reader) resolve(path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(r.base, path)
}

// refusal is the document refused at the JSON Pointer at for reason.
func (r *reader) refusal(at, reason string) error {
	if at == "" {
		return fmt.Errorf("%w: %s: %s", ErrDocument, r.file, reason)
	}
	return fmt.Errorf("%w: %s: %s: %s", ErrDocument, r.file, at, reason)
}
