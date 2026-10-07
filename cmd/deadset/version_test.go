package main

import (
	"bytes"
	"encoding/json"
	"regexp"
	"testing"

	spec "github.com/cplieger/deadset-spec/v6"
	"github.com/cplieger/deadset/internal/verdict"
)

// TestTheVersionIsTheModuleVersionTheBuildStamped pins what each shape the
// toolchain stamps becomes, and that every one of them is a version the report
// schema accepts in analyzer.version.
func TestTheVersionIsTheModuleVersionTheBuildStamped(t *testing.T) {
	t.Parallel()

	pattern := analyzerVersionPattern(t)
	for name, one := range map[string]struct {
		stamped string
		want    string
	}{
		"the tag under go install": {
			stamped: "v1.2.3", want: "1.2.3",
		},
		"the pseudo-version under go build in a checkout": {
			stamped: "v0.1.1-0.20261001120000-abcdef123456", want: "0.1.1-0.20261001120000-abcdef123456",
		},
		"a build of a tree holding uncommitted changes": {
			stamped: "v1.2.3+dirty", want: "1.2.3+dirty",
		},
		"a build the toolchain records no version for": {
			stamped: "(devel)", want: develVersion,
		},
		"a build carrying no module version at all": {
			stamped: "", want: develVersion,
		},
		"a bare v": {
			stamped: "v", want: develVersion,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := versionOf(one.stamped)
			if got != one.want {
				t.Errorf("versionOf(%q) = %q, want %q", one.stamped, got, one.want)
			}
			if !pattern.MatchString(got) {
				t.Errorf("versionOf(%q) = %q, which analyzer.version of the report schema refuses", one.stamped, got)
			}
		})
	}
}

// TestTheVersionVerbPrintsTheDevelVersionUnderTest pins the exact output of the
// version verb in a test binary, which the toolchain stamps with no module
// version.
func TestTheVersionVerbPrintsTheDevelVersionUnderTest(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	if got := run([]string{"version"}, &stdout, &stderr); got != verdict.Clean {
		t.Fatalf("run([version]) = %d, want %d; stderr: %q", got, verdict.Clean, stderr.String())
	}
	const want = "deadset 0.0.0-devel\ncontract 6.0.0\n"
	if stdout.String() != want {
		t.Errorf("run([version]) stdout = %q, want %q", stdout.String(), want)
	}
	if stderr.Len() != 0 {
		t.Errorf("run([version]) stderr = %q, want empty", stderr.String())
	}
}

// analyzerVersionPattern is the pattern contract/report.schema.json gives
// analyzer.version, which is where a merged report names this command's
// version.
func analyzerVersionPattern(t *testing.T) *regexp.Regexp {
	t.Helper()

	body, err := spec.Contract.ReadFile("contract/report.schema.json")
	if err != nil {
		t.Fatalf("Setup: read contract/report.schema.json: %v", err)
	}
	var schema struct {
		Properties struct {
			Analyzer struct {
				Properties struct {
					Version struct {
						Pattern string `json:"pattern"`
					} `json:"version"`
				} `json:"properties"`
			} `json:"analyzer"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(body, &schema); err != nil {
		t.Fatalf("Setup: decode contract/report.schema.json: %v", err)
	}
	if schema.Properties.Analyzer.Properties.Version.Pattern == "" {
		t.Fatal("Setup: contract/report.schema.json declares no pattern for analyzer.version")
	}
	return regexp.MustCompile(schema.Properties.Analyzer.Properties.Version.Pattern)
}
