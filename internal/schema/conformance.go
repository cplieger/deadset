package schema

import (
	"bytes"
	"crypto/sha256"
	_ "embed" // the results document is embedded
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/cplieger/deadset-spec/v8/report"
)

// resultsDocument is the reader's committed result over the corpus fixtures
// listing proto, which a test regenerates from the fixtures themselves.
//
//go:embed reader-results.json
var resultsDocument []byte

// Conformance is the reader's answer over the corpus, as its report's
// analyzer member carries it: the results document's corpus version and
// result, and the digest of its bytes.
func Conformance() (report.Conformance, error) {
	var held struct {
		CorpusVersion string        `json:"corpus_version"`
		Result        report.Result `json:"result"`
	}
	if err := json.NewDecoder(bytes.NewReader(resultsDocument)).Decode(&held); err != nil {
		return report.Conformance{}, fmt.Errorf("schema: read the reader's results document: %w", err)
	}
	digest := sha256.Sum256(resultsDocument)
	return report.Conformance{CorpusVersion: held.CorpusVersion, Result: held.Result, Digest: "sha256:" + hex.EncodeToString(digest[:])}, nil
}
