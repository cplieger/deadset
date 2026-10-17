package config_test

import (
	"testing"

	"github.com/cplieger/deadset-spec/v8/vocab"
	"github.com/cplieger/deadset/v2/internal/config"
)

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
