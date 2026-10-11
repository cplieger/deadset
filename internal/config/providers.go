package config

import "encoding/json"

// providerListKey is the setting that holds the provider list.
const providerListKey = "providers.analyzers"

// Provider is one entry of the provider list: an analyzer the orchestrator may run.
type Provider struct {
	// Name is the analyzer's name. No two entries of one list share it, because
	// it keys every file a run writes for the analyzer.
	Name string

	// Command is the executable as the entry writes it: a name looked up on PATH,
	// or an absolute path.
	Command string

	// Languages are the languages the analyzer claims.
	Languages []string
}

// readProviders reads the resolved provider list, in the list's order, which the
// list's check has accepted.
func readProviders(value json.RawMessage) ([]Provider, error) {
	var entries []struct {
		Name      string   `json:"name"`
		Command   string   `json:"command"`
		Languages []string `json:"languages"`
	}
	if err := json.Unmarshal(value, &entries); err != nil {
		return nil, err
	}
	list := make([]Provider, len(entries))
	for i := range entries {
		e := &entries[i]
		list[i] = Provider{Name: e.Name, Command: e.Command, Languages: e.Languages}
	}
	return list, nil
}
