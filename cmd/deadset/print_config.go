package main

import (
	"context"
	"flag"
	"io"

	"github.com/cplieger/deadset-spec/v8/vocab"
)

// bindPrintConfig binds the print-config verb, which resolves the
// configuration an analysis of the target runs under and prints it with the
// source of every setting.
func (*command) bindPrintConfig(set *flag.FlagSet) runner {
	target := set.String("target", ".", "")
	central := set.String("central", "", "")
	return func(ctx context.Context, stdout, _ io.Writer) (int, error) {
		resolved, _, err := resolveConfiguration(ctx, set, *target, *central)
		if err != nil {
			return 0, err
		}
		return vocab.ExitClean, resolved.Print(stdout)
	}
}
