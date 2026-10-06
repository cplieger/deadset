package merge

import (
	"bytes"
	"encoding/json"
	"os"
	"regexp"
	"testing"

	spec "github.com/cplieger/deadset-spec/v6"
	"github.com/cplieger/deadset/internal/report"
)

// resultsDescription is the description the results document carries.
const resultsDescription = "The merge's result over the published merge vectors of the corpus version named: " +
	"a case passes when the merge writes its expected report byte for byte with its expected exit code, " +
	"or refuses it with the refusal and the exit code it states. result is fail when any case fails."

// TestTheConformanceRecordIsTheMergesAnswerOverThePublishedVectors runs every
// published merge case and requires the committed results document to be what
// that run produces, naming the pinned corpus version: the conformance block
// a merged report carries is then the answer of this build's merge.
//
// UPDATE_GOLDEN=1 writes the document instead of comparing it.
func TestTheConformanceRecordIsTheMergesAnswerOverThePublishedVectors(t *testing.T) {
	t.Parallel()

	var corpus struct {
		CorpusVersion string `json:"corpus_version"`
	}
	body, err := spec.Corpus.ReadFile("corpus/corpus.json")
	if err != nil || json.Unmarshal(body, &corpus) != nil {
		t.Fatalf("Setup: read corpus/corpus.json: %v", err)
	}
	ran := results{Description: resultsDescription, CorpusVersion: corpus.CorpusVersion, Result: report.ResultPass}
	for _, name := range caseNames(t) {
		result := report.ResultPass
		if err := answer(readCase(t, name), name); err != nil {
			t.Errorf("case %s fails: %v", name, err)
			result, ran.Result = report.ResultFail, report.ResultFail
		}
		ran.Cases = append(ran.Cases, caseResult{Case: name, Result: result})
	}
	var written bytes.Buffer
	encoder := json.NewEncoder(&written)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(&ran); err != nil {
		t.Fatalf("encode the results document: %v", err)
	}

	const path = "merge-results.json"
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(path, written.Bytes(), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		return
	}
	if !bytes.Equal(written.Bytes(), resultsDocument) {
		t.Errorf("the run over the published vectors writes\n%s\nwant %s as committed\n%s(run UPDATE_GOLDEN=1 go test -run TestTheConformanceRecord ./internal/merge/ to record it)",
			written.Bytes(), path, resultsDocument)
	}
}

// TestConformanceIsTheCommittedRecord pins the block a merged report carries
// to the committed document: its corpus version and result, and the digest of
// its bytes in the form the report schema states.
func TestConformanceIsTheCommittedRecord(t *testing.T) {
	t.Parallel()

	got, err := Conformance()
	if err != nil {
		t.Fatalf("Conformance() = %v", err)
	}
	var held results
	if err := json.Unmarshal(resultsDocument, &held); err != nil {
		t.Fatalf("Setup: decode the results document: %v", err)
	}
	if got.CorpusVersion != held.CorpusVersion || got.Result != held.Result || got.Result != report.ResultPass {
		t.Errorf("Conformance() = %+v, want corpus version %s and result %s, a pass", got, held.CorpusVersion, held.Result)
	}
	if !regexp.MustCompile(`^sha256:[0-9a-f]{64}$`).MatchString(got.Digest) {
		t.Errorf("Conformance().Digest = %q, want sha256: and 64 lowercase hex digits", got.Digest)
	}
}
