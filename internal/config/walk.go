package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
)

// supplied is the values one source supplies, by the dotted path of the setting
// each is the value of. The severity object is one entry, under its own key.
type supplied map[string]json.RawMessage

// reader walks one configuration document against the closed key list.
type reader struct {
	keys   *node
	values supplied
	label  string
}

// readDocument walks one configuration document against the closed key list and
// returns the value it supplies for each setting. It refuses a key the list does not
// declare and a key one object names twice, at any depth, naming its dotted path: a
// decoder unmarshalling into a value can report neither, since it names an unknown
// member without its path and keeps the last of two values without a word.
func readDocument(keys *node, data []byte, label string) (supplied, error) {
	r := reader{keys: keys, values: supplied{}, label: label}
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := r.open(dec, ""); err != nil {
		return nil, err
	}
	if err := r.section(dec, keys, ""); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, malformed(label, "", "want one JSON object and nothing after it")
	}
	return r.values, nil
}

// open reads the opening of the object the value at at must be.
func (r *reader) open(dec *json.Decoder, at string) error {
	token, err := dec.Token()
	if errors.Is(err, io.EOF) {
		return malformed(r.label, at, "want an object, and the document holds nothing")
	}
	if err != nil {
		return malformed(r.label, at, "want an object: %v", err)
	}
	if delim, isDelim := token.(json.Delim); !isDelim || delim != '{' {
		return malformed(r.label, at, "want an object")
	}
	return nil
}

// section reads the members of the section the decoder has just opened at the
// dotted path at.
func (r *reader) section(dec *json.Decoder, n *node, at string) error {
	seen := make(map[string]bool)
	for dec.More() {
		name, err := r.memberName(dec, at)
		if err != nil {
			return err
		}
		path := joinKey(at, name)
		if seen[name] {
			return duplicated(r.label, path)
		}
		seen[name] = true
		key, declared := n.child(name)
		if !declared {
			return r.undeclared(path)
		}
		if err := r.member(dec, key, path); err != nil {
			return err
		}
	}
	return r.close(dec, at)
}

// memberName reads the name of the next member of the object at at.
func (r *reader) memberName(dec *json.Decoder, at string) (string, error) {
	token, err := dec.Token()
	if err != nil {
		return "", malformed(r.label, at, "%v", err)
	}
	name, isName := token.(string)
	if !isName {
		return "", malformed(r.label, at, "want a member name")
	}
	return name, nil
}

// close reads the end of the object at at.
func (r *reader) close(dec *json.Decoder, at string) error {
	if _, err := dec.Token(); err != nil {
		return malformed(r.label, at, "%v", err)
	}
	return nil
}

// undeclared refuses a key the closed key list does not declare. A key that spells
// a setting's dotted path is one setting written as one key rather than as nested
// objects, and the refusal says so.
func (r *reader) undeclared(path string) *Error {
	if slices.Contains(settingPaths(r.keys, ""), path) {
		return unimplemented(r.label, path, "it is one setting, written as nested objects")
	}
	return unimplemented(r.label, path, "")
}

// member reads the value of one declared key at the dotted path path.
func (r *reader) member(dec *json.Decoder, key *node, path string) error {
	if key.kind == section {
		if err := r.open(dec, path); err != nil {
			return err
		}
		return r.section(dec, key, path)
	}
	var value json.RawMessage
	if err := dec.Decode(&value); err != nil {
		return malformed(r.label, path, "%v", err)
	}
	if repeated, found := firstRepeated(value, path); found {
		return duplicated(r.label, repeated)
	}
	switch key.kind {
	case codes:
		if err := checkCodeNames(value, r.label); err != nil {
			return err
		}
	case annotations:
		return checkProvenance(value, r.label)
	case section, setting, passThrough:
	}
	r.values[path] = value
	return nil
}

// readFlags reads the flag document: one member per setting a flag supplied, named
// by the setting's dotted path. A path no flag supplies is refused.
func readFlags(data []byte, label string) (supplied, error) {
	r := reader{values: supplied{}, label: label}
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := r.open(dec, ""); err != nil {
		return nil, err
	}
	for dec.More() {
		path, err := r.memberName(dec, "")
		if err != nil {
			return nil, err
		}
		if _, held := r.values[path]; held {
			return nil, duplicated(label, path)
		}
		if _, carried := flagFor(path); !carried {
			return nil, unimplemented(label, path, "no flag supplies it")
		}
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return nil, malformed(label, path, "%v", err)
		}
		r.values[path] = value
	}
	if err := r.close(dec, ""); err != nil {
		return nil, err
	}
	return r.values, nil
}

// firstRepeated returns the dotted path of the first member an object inside value
// names twice, at any depth. value is one JSON value the decoder has already read
// whole, so it is well formed and no token read here fails.
func firstRepeated(value json.RawMessage, at string) (string, bool) {
	return repeatedIn(json.NewDecoder(bytes.NewReader(value)), at)
}

// repeatedIn reads one value from dec, the value at the dotted path at.
func repeatedIn(dec *json.Decoder, at string) (string, bool) {
	token, err := dec.Token()
	if err != nil {
		return "", false
	}
	switch token {
	case json.Delim('{'):
		return repeatedInObject(dec, at)
	case json.Delim('['):
		for index := 0; dec.More(); index++ {
			if path, found := repeatedIn(dec, at+"["+strconv.Itoa(index)+"]"); found {
				return path, true
			}
		}
		_, _ = dec.Token() // the closing bracket
	}
	return "", false
}

// repeatedInObject reads the members of the object the decoder has just opened at
// the dotted path at.
func repeatedInObject(dec *json.Decoder, at string) (string, bool) {
	seen := make(map[string]bool)
	for dec.More() {
		token, err := dec.Token()
		name, isName := token.(string)
		if err != nil || !isName {
			return "", false
		}
		member := joinKey(at, name)
		if seen[name] {
			return member, true
		}
		seen[name] = true
		if path, found := repeatedIn(dec, member); found {
			return path, true
		}
	}
	_, _ = dec.Token() // the closing brace
	return "", false
}

// provenanceKey and provenanceValue are the spellings the provenance object's
// members take: a setting's dotted path, and default or a source followed by the
// file or flag that supplied the value.
var (
	provenanceKey   = regexp.MustCompile(`^[a-z][a-z_]*(\.[A-Za-z0-9_]+)*$`)
	provenanceValue = regexp.MustCompile(`^(default|(repository|central|flag): [^\r\n]+)$`)
)

// checkProvenance checks a provenance object. Resolution ignores it, so a document
// can carry a printed configuration's provenance and still be read, but it is held
// to the shape the key list declares.
func checkProvenance(value json.RawMessage, label string) error {
	var entries map[string]json.RawMessage
	if !decoded(value, &entries) {
		return malformed(label, provenanceSection, "want an object")
	}
	for _, name := range sortedKeys(entries) {
		path := joinKey(provenanceSection, name)
		if !provenanceKey.MatchString(name) {
			return malformed(label, path, "is not a setting's dotted path")
		}
		var source string
		if !decoded(entries[name], &source) || !provenanceValue.MatchString(source) {
			return malformed(label, path, "want default, or a source, a colon, a space and the file or flag")
		}
	}
	return nil
}

// malformed refuses a document that is not one instance of the closed key list.
func malformed(label, path, format string, args ...any) *Error {
	detail := fmt.Sprintf(format, args...)
	if path == "" {
		return &Error{Message: label + ": " + detail}
	}
	return &Error{Key: path, Message: label + ": " + path + ": " + detail}
}

// duplicated refuses a key one object names twice, so neither value is chosen.
func duplicated(label, path string) *Error {
	return &Error{Key: path, Message: fmt.Sprintf("%s: key %q is written twice, so neither value is chosen", label, path)}
}

// unimplemented refuses a key the closed key list does not declare, with the reason
// where there is more to say than that.
func unimplemented(label, path, reason string) *Error {
	message := fmt.Sprintf("%s: key %q is not implemented", label, path)
	if reason != "" {
		message += ": " + reason
	}
	return &Error{Key: path, Message: message}
}
