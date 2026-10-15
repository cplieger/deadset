package config_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/cplieger/deadset-spec/v8/vocab"
	"github.com/cplieger/deadset/v2/internal/config"
)

// A severity key naming a kind whose severity the Contract fixes is refused
// naming every fixed code the key names, one on its own and several as a list.
func TestResolve_refusingAFixedSeverityNamesEveryFixedCodeOfTheKey(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		key, names string
	}{
		{key: "DS9004", names: "the severity of DS9004, which"},
		{key: "DS90", names: "the severity of DS9003, DS9004 and DS9006, which"},
	} {
		t.Run(tc.key, func(t *testing.T) {
			t.Parallel()

			document := `{"target": {"kind": "library"}, "severity": {"` + tc.key + `": "warn"}}`
			_, err := config.Resolve(fromRepository(document))
			refusal, isRefusal := errors.AsType[*config.Error](err)
			if !isRefusal || !strings.Contains(refusal.Message, tc.names) {
				t.Errorf("Resolve(%s) = %v, want a *config.Error naming %q", document, err, tc.names)
			}
		})
	}
}

// The severity a resolution sets for a code is its key for the code, then its
// key for the code's family, and nothing when it names neither.
func TestResolved_SeverityPrefersTheCodeOverItsFamily(t *testing.T) {
	t.Parallel()

	resolved, err := config.Resolve(fromRepository(`{"target": {"kind": "library"}, "severity": {"DS9005": "warn", "DS14": "allow", "DS10": "warn", "DS1002": "deny"}}`))
	if err != nil {
		t.Fatalf("Setup: Resolve() = %v", err)
	}
	for _, tc := range []struct {
		code  string
		want  vocab.Severity
		named bool
	}{
		{code: "DS9005", want: vocab.Warn, named: true},
		{code: "DS1401", want: vocab.Allow, named: true},
		{code: "DS1002", want: vocab.Deny, named: true},
		{code: "DS1001", want: vocab.Warn, named: true},
		{code: "DS1501"},
	} {
		got, named := resolved.Severity(tc.code)
		if got != tc.want || named != tc.named {
			t.Errorf("Severity(%q) = %q, %t, want %q, %t", tc.code, got, named, tc.want, tc.named)
		}
	}
}
