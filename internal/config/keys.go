package config

import "encoding/json"

// owner is the product a key belongs to, which decides the analyzers its value is
// handed to.
type owner uint8

const (
	// everyProduct is a key every product reads, the orchestrator included.
	everyProduct owner = iota
	// everyAnalyzer is a key every analyzer reads.
	everyAnalyzer
	// orchestrator is a key the orchestrator alone reads.
	orchestrator
	// languageAnalyzer is a language's own section, which the analyzer of the
	// language the section is named for reads.
	languageAnalyzer
)

// nodeKind is what one key of the closed key list holds.
type nodeKind uint8

const (
	// section is an object whose keys the list declares, holding no value of its
	// own.
	section nodeKind = iota
	// setting is one value, which check refuses or accepts.
	setting
	// passThrough is one value of a language's own section, which its analyzer
	// reads and checks.
	passThrough
	// codes is the severity object: one setting per issue-kind code or family it
	// names.
	codes
	// annotations is the provenance object, accepted on input and ignored by
	// resolution.
	annotations
)

// node is one key of the closed key list.
type node struct {
	// fallback is the documented default, as JSON. It is nil for a setting with
	// no default, which a source must supply.
	fallback json.RawMessage
	check    checker
	name     string
	children []node
	kind     nodeKind
	owner    owner
}

// child returns the key a section declares under name.
func (n *node) child(name string) (*node, bool) {
	for i := range n.children {
		if n.children[i].name == name {
			return &n.children[i], true
		}
	}
	return nil, false
}

// keyList returns the closed key list of the Contract's configuration schema, in
// the schema's key order, with contractVersion as the default of contract_version:
// a configuration that names no Contract version is read as written against the one
// this product implements.
func keyList(contractVersion string) node {
	return node{kind: section, owner: everyProduct, children: []node{
		{
			name: "contract_version", kind: setting, owner: everyProduct,
			fallback: quoted(contractVersion), check: matches(semanticVersion, "a semantic version"),
		},
		{name: "target", kind: section, owner: everyProduct, children: []node{
			{name: "kind", kind: setting, owner: everyProduct, check: oneOf("application", "library")},
		}},
		{name: "analysis", kind: section, owner: everyProduct, children: []node{
			{
				name: "languages", kind: setting, owner: orchestrator,
				fallback: raw(`[]`), check: list(0, oneOf("go", "ts")),
			},
			{
				name: "min_confidence", kind: setting, owner: everyAnalyzer,
				fallback: raw(`"possible"`), check: oneOf("certain", "probable", "possible"),
			},
			{
				name: "generated_files", kind: setting, owner: everyAnalyzer,
				fallback: raw(`"exclude"`), check: oneOf("exclude", "include"),
			},
			{
				name: "consumer_tests", kind: setting, owner: everyAnalyzer,
				fallback: raw(`"test"`), check: oneOf("test", "production"),
			},
			{
				name: "configurations", kind: setting, owner: everyAnalyzer,
				fallback: raw(`[]`), check: buildMatrix,
			},
			{name: "matrix", kind: section, owner: everyAnalyzer, children: []node{
				{name: "complete", kind: setting, owner: everyAnalyzer, fallback: raw(`false`), check: boolean},
			}},
			{
				name: "template_dirs", kind: setting, owner: everyAnalyzer,
				fallback: raw(`[]`), check: list(0, nonEmpty),
			},
			{
				name: "template_delimiters", kind: setting, owner: everyAnalyzer,
				fallback: raw(`{"left":"{{","right":"}}"}`), check: delimiterPair,
			},
		}},
		{name: "consumers", kind: section, owner: everyAnalyzer, children: []node{
			{name: "complete", kind: setting, owner: everyAnalyzer, fallback: raw(`false`), check: boolean},
		}},
		{name: "roots", kind: section, owner: everyAnalyzer, children: []node{
			{name: "patterns", kind: setting, owner: everyAnalyzer, fallback: raw(`[]`), check: list(0, nonEmpty)},
		}},
		{name: severitySection, kind: codes, owner: everyAnalyzer, fallback: raw(`{}`), check: checkSeverity},
		{name: "exemptions", kind: section, owner: everyAnalyzer, children: []node{
			{
				name: "disabled", kind: setting, owner: everyAnalyzer,
				fallback: raw(`[]`), check: list(0, matches(exemptionClass, "an exemption class name")),
			},
		}},
		{name: "reporters", kind: section, owner: everyProduct, children: []node{
			{
				name: "formats", kind: setting, owner: everyProduct,
				fallback: raw(`["text"]`), check: list(1, oneOf(FormatText, FormatJSON, FormatGitHub, FormatSARIF, FormatTemplate)),
			},
			{name: "sort", kind: setting, owner: everyProduct, fallback: raw(`"position"`), check: oneOf(SortPosition, SortSize)},
			{name: "cascade", kind: setting, owner: everyProduct, fallback: raw(`"roots"`), check: oneOf(CascadeRoots, CascadeFull)},
			{name: "max_findings", kind: setting, owner: everyProduct, fallback: raw(`0`), check: count},
			{name: "fail_on", kind: setting, owner: everyProduct, fallback: raw(`"deny"`), check: oneOf(Allow, Warn, Deny)},
		}},
		{name: "go", kind: section, owner: languageAnalyzer},
		{name: "ts", kind: section, owner: languageAnalyzer, children: []node{
			{name: "test_files", kind: passThrough, owner: languageAnalyzer, fallback: raw(`["**/*.test.{ts,tsx,mts,cts}"]`)},
			{name: "entry_files", kind: passThrough, owner: languageAnalyzer, fallback: raw(`[]`)},
		}},
		{name: provenanceSection, kind: annotations, owner: everyProduct},
	}}
}

// severitySection and provenanceSection are the two open objects of the key list,
// whose member names the list leaves to a pattern.
const (
	severitySection   = "severity"
	provenanceSection = "provenance"
)

// raw is a documented default written as JSON.
func raw(document string) json.RawMessage { return json.RawMessage(document) }

// quoted is a documented default that is a string.
func quoted(value string) json.RawMessage {
	document, _ := json.Marshal(value) // a string always encodes
	return document
}

// joinKey spells the dotted path of the member name of the value at at.
func joinKey(at, name string) string {
	if at == "" {
		return name
	}
	return at + "." + name
}

// settingPaths returns the dotted path of every setting below n, in the key list's
// order. A section holds no value of its own, and the severity object is one entry
// because the codes it names are open.
func settingPaths(n *node, at string) []string {
	var paths []string
	for i := range n.children {
		key := &n.children[i]
		path := joinKey(at, key.name)
		switch key.kind {
		case section:
			paths = append(paths, settingPaths(key, path)...)
		case setting, passThrough, codes:
			paths = append(paths, path)
		case annotations:
		}
	}
	return paths
}
