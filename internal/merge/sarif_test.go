package merge

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"reflect"
	"testing"

	"github.com/cplieger/deadset-spec/v8/render"
	"github.com/cplieger/deadset-spec/v8/report"
)

// A merge carries every member of a component, so the merged log relates the
// declaration a second package of one name holds under the finding's own
// reference, with the links the analyzer's own log writes.
func TestTheSARIFOfAMergedReportRelatesEveryOtherDeclarationOfTheComponent(t *testing.T) {
	t.Parallel()

	const dir = "vectors/sarif/component-member-sharing-the-finding-reference"
	own, err := report.Decode(readVector(t, dir+"/report.json"))
	if err != nil {
		t.Fatalf("Setup: decode %s/report.json: %v", dir, err)
	}
	var sources struct {
		Files map[string]string `json:"files"`
	}
	if err := json.Unmarshal(readVector(t, dir+"/sources.json"), &sources); err != nil {
		t.Fatalf("Setup: decode %s/sources.json: %v", dir, err)
	}
	read := func(path string) ([]byte, error) {
		if content, held := sources.Files[path]; held {
			return []byte(content), nil
		}
		return nil, fmt.Errorf("%s: %w", path, fs.ErrNotExist)
	}
	merged, _ := mustMerge(t, inputs(own))
	var out bytes.Buffer
	if err := render.SARIF(&out, merged, read); err != nil {
		t.Fatalf("SARIF(merged %s) = %v", dir, err)
	}

	got, want := relatedOfEachResult(t, out.Bytes()), relatedOfEachResult(t, readVector(t, dir+"/expected.json"))
	if len(got) != 1 || len(want) != 1 {
		t.Fatalf("SARIF(merged %s) holds %d results, want the one result of expected.json, which holds %d", dir, len(got), len(want))
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SARIF(merged %s) relates %+v, want the related locations and links of expected.json %+v", dir, got, want)
	}
}

// relatedOfEachResult is the message and the related locations of every
// result of a SARIF log, in run and result order, each decoded as a value.
func relatedOfEachResult(t *testing.T, document []byte) []any {
	t.Helper()

	var log struct {
		Runs []struct {
			Results []struct {
				RelatedLocations any `json:"relatedLocations"`
				Message          any `json:"message"`
			} `json:"results"`
		} `json:"runs"`
	}
	if err := json.Unmarshal(document, &log); err != nil {
		t.Fatalf("decode the SARIF log: %v", err)
	}
	var held []any
	for _, run := range log.Runs {
		for _, result := range run.Results {
			held = append(held, []any{result.Message, result.RelatedLocations})
		}
	}
	return held
}
