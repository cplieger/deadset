package render

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"unicode/utf16"
)

// The line fingerprint's constants: the unit appended after the last unit of a
// file, the number of units one line's hash covers, and the multiplier of the
// polynomial.
const (
	fingerprintSentinel = 0xffff
	fingerprintWindow   = 100
	fingerprintFactor   = 37
)

// lineHashes is the line fingerprint of every line of one file, indexed from
// zero. A code-scanning service opens a second alert where its own value for
// a line disagrees, so the procedure is the Contract's exactly: a line's hash
// is the polynomial over the hundred significant units from its start, a
// position past the sentinel counting as zero, rendered in lowercase
// hexadecimal with a colon and how many lines so far rendered the same hash.
func lineHashes(content []byte) []string {
	units := significantUnits(content)
	hashes := make([]string, 0, len(units)/16+1)
	seen := make(map[string]int)
	for _, at := range lineStarts(units) {
		var hash uint64
		for i := at; i < at+fingerprintWindow; i++ {
			var unit uint64
			if i < len(units) {
				unit = uint64(units[i])
			}
			hash = hash*fingerprintFactor + unit
		}
		rendered := strconv.FormatUint(hash, 16)
		seen[rendered]++
		hashes = append(hashes, rendered+":"+strconv.Itoa(seen[rendered]))
	}
	return hashes
}

// significantUnits is the units of one file the procedure counts, with the
// sentinel appended.
func significantUnits(content []byte) []uint16 {
	all := utf16.Encode([]rune(string(content)))
	kept := make([]uint16, 0, len(all)+1)
	afterCarriageReturn := false
	for _, unit := range all {
		switch unit {
		case ' ', '\t':
		case '\r':
			kept = append(kept, '\n')
			afterCarriageReturn = true
			continue
		case '\n':
			if !afterCarriageReturn {
				kept = append(kept, '\n')
			}
		default:
			kept = append(kept, unit)
		}
		afterCarriageReturn = false
	}
	return append(kept, fingerprintSentinel)
}

// lineStarts is the index of the first unit of every line. After a file that
// ends in a line feed the sentinel starts a line of its own, which no result
// refers to.
func lineStarts(units []uint16) []int {
	starts := []int{0}
	for i := range len(units) - 1 {
		if units[i] == '\n' {
			starts = append(starts, i+1)
		}
	}
	return starts
}

// symbolFingerprint is the key a baseline joins on, which a line move leaves
// unchanged: the digest of the code, one line feed and the symbol reference.
func symbolFingerprint(code, ref string) string {
	digest := sha256.Sum256([]byte(code + "\n" + ref))
	return hex.EncodeToString(digest[:])
}
