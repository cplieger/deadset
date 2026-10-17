package config

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"

	spec "github.com/cplieger/deadset-spec/v8"
	"github.com/cplieger/deadset-spec/v8/document"
	"github.com/cplieger/deadset-spec/v8/vocab"
)

// schemaPath is the Contract's configuration schema, which every document is
// checked against whole.
const schemaPath = "contract/config.schema.json"

var configSchema = sync.OnceValues(func() (*document.Schema, error) {
	return document.Compile(spec.Contract, schemaPath)
})

// check refuses a configuration document, labelled as a refusal names its
// source, that is not strict JSON, that the configuration schema refuses, or
// whose values break a rule the schema cannot state.
func check(data []byte, label func(vocab.Key) string) error {
	schema, err := configSchema()
	if err != nil {
		return err
	}
	value, err := document.Decode(data)
	if err != nil {
		return &Error{Message: fmt.Sprintf("%s: %v", label(""), err)}
	}
	if refused := schema.FirstRefusal(value); refused != nil {
		return refusal(label, refused, value)
	}
	return checkValues(suppliedBy(data), label)
}

// refusal is the error one schema refusal is, naming the place it refuses in
// doc, the decoded document, by its [document.Error.Key]: an undeclared member
// is a key the orchestrator does not implement.
func refusal(label func(vocab.Key) string, refused *document.Error, doc any) *Error {
	path := refused.Key("", doc)
	source := label(keyOf(path))
	switch refused.Keyword {
	case "additionalProperties":
		reason := ""
		if _, declared := vocab.ConfigKeyOf(vocab.Key(path)); declared {
			reason = "it is one setting, written as nested objects"
		}
		return unimplemented(source, path, reason)
	case "propertyNames":
		return unimplemented(source, path, "")
	case "required":
		return malformed(source, path, "is required and absent")
	default:
		return malformed(source, path, refused.Reason)
	}
}

// keyOf is the declared key the dotted path is at or inside, or "" for none.
func keyOf(path string) vocab.Key {
	for {
		if _, declared := vocab.ConfigKeyOf(vocab.Key(path)); declared || path == "" {
			return vocab.Key(path)
		}
		cut := strings.LastIndexAny(path, ".[")
		if cut < 0 {
			return ""
		}
		path = path[:cut]
	}
}

// wholeNumber is a JSON integer written with no fraction and no exponent.
var wholeNumber = regexp.MustCompile(`^-?(0|[1-9]\d*)$`)

// integer is the JSON Schema type of a whole number, and the cli.json value
// of an option that takes one.
const integer = "integer"

// checkValues refuses what the schema admits and the Contract refuses: an
// integer written with a fraction or an exponent, which a JSON Schema
// validator reads as the integer it equals, and a provider list naming one
// analyzer twice, refused at the later entry because a run keys every file it
// writes for an analyzer by the name.
func checkValues(values map[vocab.Key]json.RawMessage, label func(vocab.Key) string) error {
	keys := vocab.ConfigKeys()
	for i := range keys {
		key := &keys[i]
		value, held := values[key.Key]
		if !held {
			continue
		}
		if valueType(key) == integer && !wholeNumber.Match(value) {
			return malformed(label(key.Key), string(key.Key), "want a whole number, written with no fraction and no exponent")
		}
		if key.Key == vocab.KeyProvidersAnalyzers {
			if err := checkNames(value, label(key.Key)); err != nil {
				return err
			}
		}
	}
	return nil
}

// valueType is the JSON type the schema of a key's value names, or "".
func valueType(key *vocab.ConfigKey) string {
	var schema struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal([]byte(key.Value), &schema) // a generated schema always decodes
	return schema.Type
}

// checkNames refuses a provider list naming one analyzer twice.
func checkNames(value json.RawMessage, label string) error {
	var entries []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(value, &entries); err != nil {
		return malformed(label, string(vocab.KeyProvidersAnalyzers), err.Error())
	}
	named := make(map[string]bool, len(entries))
	for index, entry := range entries {
		if named[entry.Name] {
			return malformed(label, fmt.Sprintf("%s[%d].name", vocab.KeyProvidersAnalyzers, index),
				fmt.Sprintf("repeats the analyzer name %q of an earlier entry", entry.Name))
		}
		named[entry.Name] = true
	}
	return nil
}

// suppliedBy is the value a document the schema admits supplies for each
// setting and each keyed section, by its key.
func suppliedBy(data []byte) map[vocab.Key]json.RawMessage {
	values := map[vocab.Key]json.RawMessage{}
	var walk func(at string, section json.RawMessage)
	walk = func(at string, section json.RawMessage) {
		var members map[string]json.RawMessage
		_ = json.Unmarshal(section, &members) // the schema admitted an object here
		for name, value := range members {
			key := vocab.Key(joinKey(at, name))
			row, _ := vocab.ConfigKeyOf(key)
			switch row.Role {
			case vocab.RoleSection:
				walk(string(key), value)
			case vocab.RoleSetting, vocab.RoleKeyed:
				values[key] = value
			case vocab.RoleIgnored:
			}
		}
	}
	walk("", data)
	return values
}

// joinKey spells the dotted path of the member name below the key at at.
func joinKey(at, name string) string {
	if at == "" {
		return name
	}
	return at + "." + name
}

// malformed refuses a value at the dotted path path.
func malformed(label, path, detail string) *Error {
	if path == "" {
		return &Error{Message: label + ": " + detail}
	}
	return &Error{Message: label + ": " + path + ": " + detail}
}

// unimplemented refuses a key the closed key list does not declare, with the
// reason where there is more to say than that.
func unimplemented(label, path, reason string) *Error {
	message := fmt.Sprintf("%s: key %q is not implemented", label, path)
	if reason != "" {
		message += ": " + reason
	}
	return &Error{Message: message}
}
