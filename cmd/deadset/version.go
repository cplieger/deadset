package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"runtime/debug"
	"strings"

	"github.com/cplieger/deadset-spec/v8/vocab"
)

// develVersion is the version of a build the toolchain stamped no module
// version on: a test binary, or a build of a tree that is not under version
// control. It is shaped like every other version this command writes, because a
// merged report names the merging product's version and the report schema
// accepts a semantic version and nothing else.
const develVersion = "0.0.0-devel"

// bindVersion binds the version verb, which prints the version of this build
// and the Contract version it implements, one to a line.
func (*command) bindVersion(*flag.FlagSet) runner {
	return func(_ context.Context, stdout, _ io.Writer) (int, error) {
		_, err := fmt.Fprintf(stdout, "deadset %s\ncontract %s\n", version(), vocab.ContractVersion)
		return vocab.ExitClean, err
	}
}

// version is this command's own version, which the build carries rather than
// the source and which moves independently of [vocab.ContractVersion]. It is
// the module version the build stamped with the leading v removed, or
// develVersion for a build that records none. Every verb that names this
// command's version reads this one function.
func version() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return develVersion
	}
	return versionOf(info.Main.Version)
}

// versionOf is the version one stamped module version names. A module version
// is the letter v followed by a semantic version, so a value without that
// prefix names none: the toolchain writes (devel) for a main module it has no
// version for, and the empty string where a build carries no module version at
// all.
func versionOf(stamped string) string {
	semver, isVersion := strings.CutPrefix(stamped, "v")
	if !isVersion || semver == "" {
		return develVersion
	}
	return semver
}
