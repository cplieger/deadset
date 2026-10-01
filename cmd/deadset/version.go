package main

import (
	"fmt"
	"io"
	"runtime/debug"
	"strings"

	"github.com/cplieger/deadset/internal/report"
)

// develVersion is the version of a build the toolchain stamped no module
// version on: a test binary, or a build of a tree that is not under version
// control. It is shaped like every other version this command writes, because a
// merged report names the merging product's version and the report schema
// accepts a semantic version and nothing else.
const develVersion = "0.0.0-devel"

func init() { register("version", runVersion) }

// runVersion prints the version of this build and the Contract version it
// implements, one to a line. It reads no argument.
func runVersion(_ []string, stdout, _ io.Writer) int {
	fmt.Fprintf(stdout, "deadset %s\ncontract %s\n", version(), report.ContractVersion)
	return exitClean
}

// version is this command's own version, which the build carries rather than
// the source and which moves independently of [report.ContractVersion]. It is
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
