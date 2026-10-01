package config

import (
	"encoding/json"
	"flag"
	"strings"
)

// settingFlag is one command-line flag that supplies one setting. The name is the
// product's own, chosen for how it reads on a command line; path is the dotted path
// of the setting it supplies. A list flag takes its entries separated by commas.
type settingFlag struct {
	name  string
	path  string
	usage string
	list  bool
}

// settingFlags are the settings the command line may supply.
var settingFlags = []settingFlag{
	{name: "fail-on", path: "reporters.fail_on", usage: "the lowest severity that fails the run: allow, warn or deny"},
	{
		name: "formats", path: "reporters.formats", list: true,
		usage: "the output formats, separated by commas: text, json, github, sarif, template",
	},
	{
		name: "languages", path: "analysis.languages", list: true,
		usage: "the languages in scope, separated by commas; empty detects them",
	},
	{
		name: "min-confidence", path: "analysis.min_confidence",
		usage: "the lowest reachability class a finding is reported at: certain, probable or possible",
	},
}

// flagFor returns the flag that supplies the setting at path.
func flagFor(path string) (settingFlag, bool) {
	for _, f := range settingFlags {
		if f.path == path {
			return f, true
		}
	}
	return settingFlag{}, false
}

// RegisterFlags defines on set one flag per setting the command line may supply.
func RegisterFlags(set *flag.FlagSet) {
	for _, f := range settingFlags {
		set.String(f.name, "", f.usage)
	}
}

// FlagSettings returns the settings the flags [RegisterFlags] defined on set
// supplied on the command line set parsed, as the document [Inputs] Flags carries,
// or nil when the command line set none of them. A flag set to the empty string
// supplies the empty string, or, for a list, the empty list.
func FlagSettings(set *flag.FlagSet) ([]byte, error) {
	settings := make(map[string]any)
	set.Visit(func(given *flag.Flag) {
		for _, f := range settingFlags {
			if f.name == given.Name {
				settings[f.path] = f.value(given.Value.String())
			}
		}
	})
	if len(settings) == 0 {
		return nil, nil
	}
	return json.Marshal(settings)
}

// value is the setting a flag's text supplies.
func (f *settingFlag) value(text string) any {
	if !f.list {
		return text
	}
	entries := []string{}
	if text == "" {
		return entries
	}
	for entry := range strings.SplitSeq(text, ",") {
		entries = append(entries, strings.TrimSpace(entry))
	}
	return entries
}
