package source

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/cplieger/deadset-spec/v8/facts"
	"github.com/cplieger/deadset-spec/v8/report"
	"github.com/cplieger/deadset-spec/v8/suppress"
	"github.com/cplieger/deadset-spec/v8/vocab"
	"github.com/cplieger/deadset/v2/internal/rundir"
	"github.com/cplieger/deadset/v2/internal/schema"
)

var _ Source = (*InProcess)(nil)

// InProcess is the schema reader, linked into this program and run in it.
type InProcess struct {
	digest string
	in     schema.Input
}

// NewInProcess is the reader of the target at the absolute path target, its
// report naming the target as root and identity, at deadset's version, in a
// run whose analyzers read analyzed.
func NewInProcess(target, root, identity, version string, analyzed []vocab.Language) *InProcess {
	return &InProcess{in: schema.Input{Target: target, Root: root, Identity: identity, Version: version, Analyzed: analyzed}}
}

// Replan is the reader s, as admitted, over the target named root and
// identity in a run whose analyzers read analyzed.
func (s *InProcess) Replan(root, identity string, analyzed []vocab.Language) *InProcess {
	again := *s
	again.in.Root, again.in.Identity, again.in.Analyzed = root, identity, analyzed
	return &again
}

// Name is the reader's name, the proto row's analyzer.
func (*InProcess) Name() string { return schema.Name }

// Admit digests this program's executable, which runs the reader, reads the
// reader's conformance record and keeps the describe document a process
// analyzer would have printed. It refuses nothing the reader itself states.
func (s *InProcess) Admit(ctx context.Context, kept rundir.Entry, _ []string) error {
	refuse := func(err error) error { return &HandshakeError{Err: err, Analyzer: schema.Name} }
	executable, err := os.Executable()
	if err != nil {
		return refuse(err)
	}
	if s.digest, err = artifactDigest(executable); err != nil {
		return refuse(err)
	}
	if s.in.Conformance, err = schema.Conformance(); err != nil {
		return refuse(err)
	}
	described, err := json.MarshalIndent(map[string]any{
		"name": schema.Name, "version": s.in.Version, "contract_version": vocab.ContractVersion,
		"schema_versions_accepted": vocab.SchemaVersions(), "facts_version_written": vocab.FactsVersion,
		"languages":   []vocab.Language{vocab.Proto},
		"conformance": s.in.Conformance,
	}, "", "  ")
	if err != nil {
		return refuse(err)
	}
	return kept.WriteDescribe(ctx, append(described, '\n'))
}

// Produce reads the configuration files.Config holds and the target, and
// writes the report at files.Report. A setup failure or a malformed
// suppression ends it as the analyzer CLI contract's exit code would, with
// the line a process would print.
func (s *InProcess) Produce(ctx context.Context, files Files) Outcome {
	out := Outcome{Analyzer: schema.Name, Languages: []vocab.Language{vocab.Proto}, Digest: s.digest, Exit: -1}
	refuse := func(err error) Outcome {
		out.Err = &Error{Err: err, Analyzer: schema.Name, Report: files.Report, Exit: out.Exit}
		return out
	}
	data, err := os.ReadFile(files.Config)
	if err != nil {
		return refuse(err)
	}
	in := s.in
	if in.Settings, err = schema.ParseSettings(data); err != nil {
		return refuse(err)
	}
	result, err := schema.Read(ctx, &in)
	var setup *schema.SetupError
	switch {
	case errors.As(err, &setup):
		out.Exit, out.Printed = vocab.ExitFailure, []byte(vocab.SetupFailurePrefix+setup.Error()+"\n")
		return refuse(fmt.Errorf("exited %d: %w", out.Exit, ErrExited))
	case errors.Is(err, suppress.ErrMalformed):
		out.Exit, out.Printed = vocab.ExitUsage, []byte(err.Error()+"\n")
		return refuse(fmt.Errorf("exited %d, a usage or configuration error of the analyzer: %w", out.Exit, ErrExited))
	case err != nil:
		return refuse(err)
	}
	var written bytes.Buffer
	if err := report.Encode(&written, result.Report); err != nil {
		return refuse(err)
	}
	if err := rundir.WriteFile(ctx, files.Report, written.Bytes()); err != nil {
		return refuse(err)
	}
	if files.Facts != "" {
		var held bytes.Buffer
		if err := facts.Encode(&held, result.Facts); err != nil {
			return refuse(err)
		}
		if err := rundir.WriteFile(ctx, files.Facts, held.Bytes()); err != nil {
			return refuse(err)
		}
	}
	out.Exit, out.Report, out.Facts = vocab.ExitClean, result.Report, result.Facts
	return out
}
