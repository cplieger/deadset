package config

import (
	"encoding/json"
	"flag"
	"slices"
	"strings"

	"github.com/cplieger/deadset-spec/v8/vocab"
)

// RegisterFlags defines on set one flag for each of options that supplies a
// setting, named and described as cli.json states the option.
func RegisterFlags(set *flag.FlagSet, options []string) {
	keys := vocab.ConfigKeys()
	for i := range keys {
		if key := &keys[i]; key.Option != "" && slices.Contains(options, key.Option) {
			option, _ := vocab.OptionOf(key.Option)
			set.String(option.Name, "", option.Summary)
		}
	}
}

// FlagSettings returns the setting each flag [RegisterFlags] defined on set
// supplied on the command line set parsed, by its key, as [Inputs] Flags
// carries them. A list option takes its entries separated by commas, and the
// empty string is the empty list; an integer option written as JSON supplies
// that value, for the configuration schema to admit or refuse.
func FlagSettings(set *flag.FlagSet) map[vocab.Key]json.RawMessage {
	settings := map[vocab.Key]json.RawMessage{}
	set.Visit(func(given *flag.Flag) {
		if key, supplied := optionKey(given.Name); supplied {
			settings[key] = flagValue(given.Name, given.Value.String())
		}
	})
	return settings
}

// optionKey is the setting the option of that name supplies.
func optionKey(name string) (vocab.Key, bool) {
	keys := vocab.ConfigKeys()
	for i := range keys {
		if keys[i].Option == name {
			return keys[i].Key, true
		}
	}
	return "", false
}

// flagValue is the JSON value the text an option of that name was given
// supplies.
func flagValue(name, text string) json.RawMessage {
	option, _ := vocab.OptionOf(name)
	var value any = text
	switch option.Value {
	case "list":
		entries := []string{}
		if text != "" {
			for entry := range strings.SplitSeq(text, ",") {
				entries = append(entries, strings.TrimSpace(entry))
			}
		}
		value = entries
	case integer:
		if json.Valid([]byte(text)) {
			return json.RawMessage(text)
		}
	}
	encoded, _ := json.Marshal(value) // a string or a list of strings always encodes
	return encoded
}
