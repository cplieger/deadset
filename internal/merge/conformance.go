package merge

import (
	"bytes"
	"crypto/sha256"
	_ "embed" // the results document is embedded
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/cplieger/deadset/internal/report"
)

// resultsDocument is the merge's committed result over the published merge
// vectors, which a test regenerates from the cases themselves.
//
//go:embed merge-results.json
var resultsDocument []byte

// results is the results document, member for member.
type results struct {
	Description   string        `json:"description"`
	CorpusVersion string        `json:"corpus_version"`
	Result        report.Result `json:"result"`
	Cases         []caseResult  `json:"cases"`
}

// caseResult is the merge's result over one published case.
type caseResult struct {
	Case   string        `json:"case"`
	Result report.Result `json:"result"`
}

// Conformance is the merge's answer over the published merge vectors, in the
// form a merged report's analyzer member carries: the corpus version the
// results document names, its result, and the digest of its bytes.
func Conformance() (report.Conformance, error) {
	var held results
	decoder := json.NewDecoder(bytes.NewReader(resultsDocument))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&held); err != nil {
		return report.Conformance{}, fmt.Errorf("merge: read the merge's results document: %w", err)
	}
	digest := sha256.Sum256(resultsDocument)
	return report.Conformance{
		CorpusVersion: held.CorpusVersion,
		Result:        held.Result,
		Digest:        "sha256:" + hex.EncodeToString(digest[:]),
	}, nil
}
