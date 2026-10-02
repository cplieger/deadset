package render

import (
	"slices"
	"strings"
	"testing"
)

// The line fingerprint of the Contract's five-line vector is the value the
// Contract publishes for each line, whether the fourth line is indented by a
// tab or by spaces and whether the lines end in LF or CR LF.
func TestLineHashesAreTheContractsVector(t *testing.T) {
	t.Parallel()

	want := []string{
		"7a0e51a45e6d7320:1", "3134adfd1bbad887:1", "247cff8f02e0b919:1", "58228fc5cbc49530:1", "32dce9ccfdbc9d3e:1",
	}
	for name, file := range map[string]string{
		"tab":    "package fixture\n\nfunc Ünused() {\n\treturn\n}\n",
		"spaces": "package fixture\n\nfunc Ünused() {\n    return\n}\n",
		"crlf":   "package fixture\r\n\r\nfunc Ünused() {\r\n\treturn\r\n}\r\n",
	} {
		got := lineHashes([]byte(file))
		if len(got) < len(want) || !slices.Equal(got[:len(want)], want) {
			t.Errorf("lineHashes(%s %q) = %q, want the first five lines %q", name, file, got, want)
		}
	}
}

// Identical windows are told apart by the counter, and the windows the
// sentinel and the zero padding enter render a hash of their own.
func TestLineHashesCountRepeatedWindows(t *testing.T) {
	t.Parallel()

	got := lineHashes([]byte(strings.Repeat("y\n", 200)))
	for line, want := range map[int]string{1: "43762f342805c306:1", 2: "43762f342805c306:2", 151: "43762f342805c306:151"} {
		if got[line-1] != want {
			t.Errorf("lineHashes(200 lines of y) line %d = %q, want %q", line, got[line-1], want)
		}
	}
	if hash, _, _ := strings.Cut(got[151], ":"); hash == "43762f342805c306" {
		t.Errorf("lineHashes(200 lines of y) line 152 = %q, want a hash of its own once the sentinel enters its window", got[151])
	}
}

// The symbol fingerprint is the digest the Contract publishes for each of its
// two vectors.
func TestSymbolFingerprintIsTheContractsDigest(t *testing.T) {
	t.Parallel()

	for _, c := range []struct{ code, ref, want string }{
		{code: "DS1001", ref: "go://example.com/fixture#Ünused", want: "d073714ada8cfcbee49bd5430446d6be7b837b6fd1fc34e6aa82be03b589c18d"},
		{code: "DS1001", ref: "go://example.com/app#Catalog.ResolveAlias", want: "3969945e4504f5d8a52415a6f7b4f233d1ba4820a2fe24617a3611e2535d5e32"},
	} {
		if got := symbolFingerprint(c.code, c.ref); got != c.want {
			t.Errorf("symbolFingerprint(%q, %q) = %s, want %s", c.code, c.ref, got, c.want)
		}
	}
}
