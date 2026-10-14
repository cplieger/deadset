package report

import (
	"errors"
	"strings"
	"testing"

	spec "github.com/cplieger/deadset-spec/v8"
)

const listedReport = "vectors/sarif/component-member-sharing-the-finding-reference/report.json"

// A report lists a component's members once, beside findings that name the
// component by its identifier alone.
func TestDecodeReadsAComponentListing(t *testing.T) {
	t.Parallel()

	decoded, err := Decode(read(t, spec.Vectors, listedReport))
	if err != nil {
		t.Fatalf("Decode(%s) = %v, want the report admitted", listedReport, err)
	}
	listed := decoded.ListedMembers()
	if got := len(listed["deadset-ts/c-1"]); got != 3 {
		t.Errorf("Decode(%s).ListedMembers()[deadset-ts/c-1] holds %d members, want 3", listedReport, got)
	}
}

// A finding carries no member list of its own, and a report lists each component
// its findings name once, and no other.
func TestDecodeRefusesMembersOutsideOneListingPerComponent(t *testing.T) {
	t.Parallel()

	valid := string(read(t, spec.Vectors, listedReport))
	for name, tt := range map[string]struct {
		old, new, pointer string
	}{
		"members on a finding": {
			old:     `"deletable_lines": 7`,
			new:     `"deletable_lines": 7, "members": []`,
			pointer: "/findings/0/component/members",
		},
		"one component listed twice": {
			old:     `"components": [`,
			new:     `"components": [{"id": "deadset-ts/c-1", "members": []}, `,
			pointer: "/components/1",
		},
		"a finding naming a component the listing lacks": {
			old:     `"id": "deadset-ts/c-1",`,
			new:     `"id": "deadset-ts/c-9",`,
			pointer: "/findings/0",
		},
		"a listing naming a component no finding names": {
			old:     `"components": [`,
			new:     `"components": [{"id": "deadset-ts/c-9", "members": []}, `,
			pointer: "/components/0",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			edited := strings.Replace(valid, tt.old, tt.new, 1)
			if edited == valid {
				t.Fatalf("Setup: %s holds no %s", listedReport, tt.old)
			}
			_, err := Decode([]byte(edited))
			var refused *Error
			if !errors.As(err, &refused) || refused.Pointer != tt.pointer {
				t.Errorf("Decode(%s) = %v, want a refusal at %s", name, err, tt.pointer)
			}
		})
	}
}
