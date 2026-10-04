package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// rejection is why one setting refuses a value. at is the place inside the value it
// names, relative to the setting: empty for the value itself, a member as ".right",
// an entry as "[1]". undeclared marks a member the closed key list does not declare,
// which is refused as an unimplemented key.
type rejection struct {
	at         string
	detail     string
	undeclared bool
}

// checker accepts one value of a setting, or rejects it.
type checker func(json.RawMessage) *rejection

// reject is the rejection of the value itself.
func reject(format string, args ...any) *rejection {
	return &rejection{detail: fmt.Sprintf(format, args...)}
}

// under places the rejection of one part of a value at that part. No rejection
// stays none.
func (r *rejection) under(at string) *rejection {
	if r == nil {
		return nil
	}
	return &rejection{at: at + r.at, detail: r.detail, undeclared: r.undeclared}
}

// atKey is c with a rejection of one part of the value placed at the setting
// itself, the part named in its detail: the refusal names the key, as the
// Contract's configuration cases spell it for a component extension.
func atKey(c checker) checker {
	return func(value json.RawMessage) *rejection {
		rejected := c(value)
		if rejected == nil || rejected.at == "" {
			return rejected
		}
		return &rejection{detail: "entry " + rejected.at + ": " + rejected.detail, undeclared: rejected.undeclared}
	}
}

// first returns the first of several rejections, or none when every check
// accepted.
func first(rejections ...*rejection) *rejection {
	for _, r := range rejections {
		if r != nil {
			return r
		}
	}
	return nil
}

// semanticVersion, exemptionClass, componentExtension and projectPath are the
// spellings the key list declares for contract_version, an exemption class name,
// a component file's extension, and the compiler configuration file a project
// entry of the build matrix names: a path below the target root whose segments
// are joined by /, none of them empty, . or .., and none holding a backslash or a
// line break.
var (
	semanticVersion    = regexp.MustCompile(`^\d+\.\d+\.\d+$`)
	exemptionClass     = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	componentExtension = regexp.MustCompile(`^\.[A-Za-z0-9]+$`)
	projectPath        = regexp.MustCompile(`^(?:[^/\\.\r\n][^/\\\r\n]*|\.[^/\\.\r\n][^/\\\r\n]*|\.\.[^/\\\r\n]+)(?:/(?:[^/\\.\r\n][^/\\\r\n]*|\.[^/\\.\r\n][^/\\\r\n]*|\.\.[^/\\\r\n]+))*$`)
)

// decoded decodes one JSON value into v and reports whether it could. It refuses
// null, which no key of the list admits and which the decoder would otherwise read
// as no value at all.
func decoded[T any](value json.RawMessage, v *T) bool {
	return !bytes.Equal(bytes.TrimSpace(value), []byte("null")) && json.Unmarshal(value, v) == nil
}

// text decodes a value that must be a string.
func text(value json.RawMessage) (string, *rejection) {
	var s string
	if !decoded(value, &s) {
		return "", reject("want a string")
	}
	return s, nil
}

// oneOf accepts a string from a closed set.
func oneOf[T ~string](allowed ...T) checker {
	return func(value json.RawMessage) *rejection {
		s, rejected := text(value)
		if rejected != nil {
			return rejected
		}
		if !slices.Contains(allowed, T(s)) {
			return reject("%q is not one of %s", s, spell(allowed))
		}
		return nil
	}
}

// matches accepts a string of the spelling pattern states, described as what.
func matches(pattern *regexp.Regexp, what string) checker {
	return func(value json.RawMessage) *rejection {
		s, rejected := text(value)
		if rejected != nil {
			return rejected
		}
		if !pattern.MatchString(s) {
			return reject("%q is not %s", s, what)
		}
		return nil
	}
}

// nonEmpty accepts a string of at least one character.
func nonEmpty(value json.RawMessage) *rejection {
	s, rejected := text(value)
	if rejected != nil {
		return rejected
	}
	if s == "" {
		return reject("is empty")
	}
	return nil
}

// boolean accepts true or false.
func boolean(value json.RawMessage) *rejection {
	var b bool
	if !decoded(value, &b) {
		return reject("want true or false")
	}
	return nil
}

// count accepts a whole number of zero or more.
func count(value json.RawMessage) *rejection {
	var n int
	if !decoded(value, &n) {
		return reject("want a whole number")
	}
	if n < 0 {
		return reject("%d is below the minimum of 0", n)
	}
	return nil
}

// list accepts an array of at least minItems strings, no two the same, each of
// which entry accepts.
func list(minItems int, entry checker) checker {
	return func(value json.RawMessage) *rejection {
		var entries []json.RawMessage
		if !decoded(value, &entries) {
			return reject("want an array")
		}
		if len(entries) < minItems {
			return reject("holds %d entries, want at least %d", len(entries), minItems)
		}
		seen := make(map[string]bool, len(entries))
		for index, held := range entries {
			at := "[" + strconv.Itoa(index) + "]"
			if rejected := entry(held); rejected != nil {
				return rejected.under(at)
			}
			s, rejected := text(held)
			if rejected != nil {
				return rejected.under(at)
			}
			if seen[s] {
				return reject("repeats %q", s).under(at)
			}
			seen[s] = true
		}
		return nil
	}
}

// members decodes an object value, refusing a member the key list does not declare
// for it.
func members(value json.RawMessage, declared []string) (map[string]json.RawMessage, *rejection) {
	var object map[string]json.RawMessage
	if !decoded(value, &object) {
		return nil, reject("want an object")
	}
	for _, name := range sortedKeys(object) {
		if !slices.Contains(declared, name) {
			return nil, &rejection{at: "." + name, undeclared: true}
		}
	}
	return object, nil
}

// required checks a member a shape requires: present, and accepted by check.
func required(object map[string]json.RawMessage, name string, check checker) *rejection {
	value, held := object[name]
	if !held {
		return &rejection{at: "." + name, detail: "is required and absent"}
	}
	return check(value).under("." + name)
}

// delimiterPair accepts the action delimiter pair: both members, because a pair
// with one member set names no delimiters at all.
func delimiterPair(value json.RawMessage) *rejection {
	pair, rejected := members(value, []string{"left", "right"})
	if rejected != nil {
		return rejected
	}
	return first(required(pair, "left", nonEmpty), required(pair, "right", nonEmpty))
}

// The members a build matrix entry names: a platform names an operating system, an
// architecture and the build tags in effect, a project one compiler configuration
// file, and both name the identifier.
var (
	entryMembers    = []string{"id", "os", "arch", "tags", "project"}
	platformMembers = []string{"os", "arch", "tags"}
)

// shapes states the two shapes, as a refusal of an entry taking neither or both
// names them.
const shapes = "an entry is a platform, naming id, os, arch and optionally tags, or a project, naming id and project"

// buildMatrix accepts the build matrix: an array of entries, each in exactly one of
// the two shapes.
func buildMatrix(value json.RawMessage) *rejection {
	var entries []json.RawMessage
	if !decoded(value, &entries) {
		return reject("want an array")
	}
	for index, entry := range entries {
		if rejected := configuration(entry); rejected != nil {
			return rejected.under("[" + strconv.Itoa(index) + "]")
		}
	}
	return nil
}

// configuration accepts one entry of the build matrix. Its shape is read from the
// members it names, so an entry naming a member of each shape, or of neither, is
// refused rather than read as the one its values happen to fill.
func configuration(value json.RawMessage) *rejection {
	entry, rejected := members(value, entryMembers)
	if rejected != nil {
		return rejected
	}
	platform := slices.ContainsFunc(platformMembers, func(name string) bool { _, held := entry[name]; return held })
	_, project := entry["project"]
	switch {
	case platform && project:
		return reject("names members of both shapes; %s", shapes)
	case platform:
		rejected = first(required(entry, "id", nonEmpty), required(entry, "os", nonEmpty), required(entry, "arch", nonEmpty))
		if tags, held := entry["tags"]; held && rejected == nil {
			rejected = list(0, nonEmpty)(tags).under(".tags")
		}
		return rejected
	case project:
		return first(required(entry, "id", nonEmpty),
			required(entry, "project", matches(projectPath, "a file path below the target root, its segments joined by /")))
	default:
		return reject("names neither shape; %s", shapes)
	}
}

// The members a provider entry names: an installed analyzer names its name, the
// languages it claims and its command, and an acquirable one names, beside those, the
// artifact acquisition fetches.
var (
	providerMembers    = []string{"name", languagesKey, "command", "source", "version", "digest"}
	acquisitionMembers = []string{"source", "version", "digest"}
)

// languagesKey names the languages in scope in the analysis section and the
// languages a provider entry claims.
const languagesKey = "languages"

// The spellings the key list declares for a provider entry's analyzer name, its
// source, its version and the digest of its artifact.
var (
	analyzerName     = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)
	artifactSource   = regexp.MustCompile(`^(go|npm):[^ \t\r\n]+$`)
	artifactVersion  = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)
	artifactChecksum = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

// providerList accepts the provider list: an array of entries, each in one of the
// two shapes, no two of them naming one analyzer. A repeated name is refused at the
// later entry, because a run keys every file it writes for an analyzer by the name.
func providerList(value json.RawMessage) *rejection {
	var entries []json.RawMessage
	if !decoded(value, &entries) {
		return reject("want an array")
	}
	named := make(map[string]bool, len(entries))
	for index, entry := range entries {
		at := "[" + strconv.Itoa(index) + "]"
		name, rejected := provider(entry)
		if rejected != nil {
			return rejected.under(at)
		}
		if named[name] {
			return reject("repeats the analyzer name %q of an earlier entry", name).under(at + ".name")
		}
		named[name] = true
	}
	return nil
}

// provider accepts one provider entry and returns the analyzer name it gives. An
// entry naming any member of the artifact is an acquirable analyzer and must name
// all three.
func provider(value json.RawMessage) (string, *rejection) {
	entry, rejected := members(value, providerMembers)
	if rejected != nil {
		return "", rejected
	}
	rejected = first(
		required(entry, "name", matches(analyzerName, "lowercase words joined by single hyphens")),
		required(entry, languagesKey, list(1, oneOf("go", "ts"))),
		required(entry, "command", nonEmpty),
	)
	acquirable := slices.ContainsFunc(acquisitionMembers, func(name string) bool { _, held := entry[name]; return held })
	if rejected == nil && acquirable {
		rejected = first(
			required(entry, "source", matches(artifactSource, "go: or npm: followed by the package")),
			required(entry, "version", matches(artifactVersion, "a semantic version with no leading v")),
			required(entry, "digest", matches(artifactChecksum, "sha256: followed by 64 lowercase hexadecimal digits")),
		)
	}
	if rejected != nil {
		return "", rejected
	}
	name, _ := text(entry["name"]) // the name check above accepted a string
	return name, nil
}

// spell lists a closed set as a refusal names it.
func spell[T ~string](values []T) string {
	quoted := make([]string, len(values))
	for index, value := range values {
		quoted[index] = strconv.Quote(string(value))
	}
	return strings.Join(quoted, ", ")
}

// sortedKeys returns an object's member names in ascending order, so the first of
// several refusals is the same on every run.
func sortedKeys[V any](object map[string]V) []string {
	return slices.Sorted(maps.Keys(object))
}
