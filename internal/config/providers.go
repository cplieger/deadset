package config

import "encoding/json"

// providerListKey is the setting that holds the provider list.
const providerListKey = "providers.analyzers"

// Provider is one entry of the provider list: an analyzer the orchestrator may run.
type Provider struct {
	// Artifact is what acquisition fetches for an acquirable analyzer. It is nil
	// for an installed analyzer, which is run from its command and never fetched.
	Artifact *Artifact

	// Name is the analyzer's name. No two entries of one list share it, because
	// it keys every file a run writes for the analyzer.
	Name string

	// Command is the executable as the entry writes it: a name looked up on PATH,
	// or an absolute path.
	Command string

	// Languages are the languages the analyzer claims.
	Languages []string
}

// Artifact names the artifact of an acquirable analyzer.
type Artifact struct {
	Source  string // go: or npm: followed by the package
	Version string // the semantic version acquisition fetches, with no leading v
	Digest  string // sha256: followed by the lowercase hexadecimal digest of the artifact
}

// readProviders reads the resolved provider list, in the list's order. The list's
// check has accepted it, so each entry names either all three members of the
// artifact or none of them.
func readProviders(value json.RawMessage) ([]Provider, error) {
	var entries []struct {
		Name      string   `json:"name"`
		Command   string   `json:"command"`
		Source    string   `json:"source"`
		Version   string   `json:"version"`
		Digest    string   `json:"digest"`
		Languages []string `json:"languages"`
	}
	if err := json.Unmarshal(value, &entries); err != nil {
		return nil, err
	}
	list := make([]Provider, len(entries))
	for i := range entries {
		e := &entries[i]
		list[i] = Provider{Name: e.Name, Command: e.Command, Languages: e.Languages}
		if e.Source != "" {
			list[i].Artifact = &Artifact{Source: e.Source, Version: e.Version, Digest: e.Digest}
		}
	}
	return list, nil
}
