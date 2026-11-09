package schema

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strings"

	"github.com/cplieger/deadset-spec/v8/report"
	"github.com/cplieger/deadset-spec/v8/vocab"
	"github.com/cplieger/deadset/v2/internal/documents"
)

// edges is schemas.22: the reader evaluates every side of a declared edge
// that names a proto:// reference, live when its record is, dead with the
// record's pending finding, and absent where no declaration has it, unless
// a schema file of the target is not read cleanly and may declare it, which
// schemas.4 reads as live.
func (r *reading) edges(ctx context.Context) ([]report.EdgeEvaluation, error) {
	data, err := documents.Read(ctx, r.in.Target, vocab.EdgesFileName)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("schema: %w", err)
	}
	var doc struct {
		Edges map[string]map[report.Side]string `json:"edges"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("schema: %s: %w", vocab.EdgesFileName, err)
	}
	var out []report.EdgeEvaluation
	for _, id := range slices.Sorted(maps.Keys(doc.Edges)) {
		for _, side := range []report.Side{report.SideProvides, report.SideUsedBy} {
			if ref := doc.Edges[id][side]; strings.HasPrefix(ref, "proto://") {
				out = append(out, r.evaluate(id, side, ref))
			}
		}
	}
	return out, nil
}

func (r *reading) evaluate(id string, side report.Side, ref string) report.EdgeEvaluation {
	e := report.EdgeEvaluation{Edge: id, Side: side, Symbol: ref, State: report.StateAbsent}
	if len(r.unclean) > 0 {
		e.State = report.StateLive
	}
	if d := r.byRef[ref]; d != nil {
		e.State = report.StateLive
		if d.held == "" {
			e.State, e.Finding = report.StateDead, d.finding
		}
	}
	return e
}
