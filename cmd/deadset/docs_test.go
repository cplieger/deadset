package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"

	"github.com/cplieger/deadset-spec/v8/vocab"
)

// documentBound is a page's statement of a document's size bound.
var documentBound = regexp.MustCompile(`(\d+) MiB`)

// TestTheDocumentedDocumentBoundIsTheContracts refuses a page stating a
// document bound other than vocab.DocumentMaxBytes, which every document
// deadset reads shares. Each page states it once per document family it
// describes.
func TestTheDocumentedDocumentBoundIsTheContracts(t *testing.T) {
	t.Parallel()

	if vocab.DocumentMaxBytes%(1<<20) != 0 {
		t.Fatalf("vocab.DocumentMaxBytes = %d, not a whole number of MiB the pages can state", vocab.DocumentMaxBytes)
	}
	want := strconv.Itoa(vocab.DocumentMaxBytes >> 20)
	for page, statements := range map[string]int{"commands.md": 2, "scope.md": 1} {
		path := filepath.Join("..", "..", "docs", page)
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("Setup: read %s: %v", path, err)
		}
		stated := documentBound.FindAllStringSubmatch(string(body), -1)
		if len(stated) != statements {
			t.Errorf("docs/%s states a document bound %d times, want %d", page, len(stated), statements)
		}
		for _, s := range stated {
			if s[1] != want {
				t.Errorf("docs/%s states a bound of %s MiB, want %s MiB, the bound the contract sets", page, s[1], want)
			}
		}
	}
}
