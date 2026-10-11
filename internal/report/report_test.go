package report

import "testing"

// The withheld line names the probable and the possible count, each only where
// it is not 0, and the lowest confidence it names as the setting that shows
// them all; a run that withheld nothing it would name writes no line.
func TestTheWithheldLineNamesEachCountItHolds(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		withheld Withheld
		want     string
	}{
		{name: "none", want: ""},
		{
			name: "probable only", withheld: Withheld{Probable: 1},
			want: "withheld by analysis.min_confidence: 1 probable, shown with analysis.min_confidence set to probable",
		},
		{
			name: "possible only", withheld: Withheld{Possible: 5},
			want: "withheld by analysis.min_confidence: 5 possible, shown with analysis.min_confidence set to possible",
		},
		{
			name: "both", withheld: Withheld{Probable: 2, Possible: 5},
			want: "withheld by analysis.min_confidence: 2 probable, 5 possible, shown with analysis.min_confidence set to possible",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.withheld.Line(); got != c.want {
				t.Errorf("%+v.Line() = %q, want %q", c.withheld, got, c.want)
			}
		})
	}
}
