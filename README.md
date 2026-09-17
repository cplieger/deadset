# deadset

[![Go Reference](https://pkg.go.dev/badge/github.com/cplieger/deadset.svg)](https://pkg.go.dev/github.com/cplieger/deadset)
[![Go version](https://img.shields.io/github/go-mod/go-version/cplieger/deadset)](https://github.com/cplieger/deadset/blob/main/go.mod)
[![Mutation](https://img.shields.io/endpoint?url=https://raw.githubusercontent.com/cplieger/deadset/badges/mutation.json)](https://github.com/cplieger/deadset/issues?q=label%3Agremlins-tracker)

One command for dead code across Go and TypeScript, resolving the references that cross the language boundary.

## What it does

Dead code is a declaration nothing reaches: a function nobody calls, an export nothing imports, a file no build includes, a dependency no import needs. Each language has its own analyzer for that question. `deadset` is the command you run when a repository holds more than one language, and it does four things:

1. Detects which languages the repository contains, and runs only the analyzers for those. A repository with one language never needs the other language's toolchain.
2. Runs each analyzer as a separate process. `deadset` contains no parser and no type checker of its own; every finding comes from an analyzer.
3. Resolves the references that cross the language boundary. A Go type consumed only through the TypeScript client generated from it has no reference in Go and no declaration in TypeScript, so each analyzer alone would judge it wrong. `deadset` reads both reports, follows the declared edge between the two symbols, and reports the pair as dead only when both sides are.
4. Merges the reports into one and returns one exit code, so a CI step reads one result.

The analyzers are complete programs on their own. [deadset-go](https://github.com/cplieger/deadset-go) analyzes a Go module and [deadset-ts](https://github.com/cplieger/deadset-ts) analyzes a TypeScript or JavaScript package, each from parsing to exit code. Running one directly produces the same findings as running it through `deadset`, so this command is never a required hop; it earns its place when there is a language boundary to resolve.

Every analyzer implements the same contract, published at [deadset-spec](https://github.com/cplieger/deadset-spec): one vocabulary of issue kinds, one report schema, one suppression grammar, one exit-code table and one merge definition. Any analyzer that passes that repository's conformance corpus can be listed in `deadset`'s provider list, including one written by someone else for a language neither first-party analyzer covers.

## Status

Pre-release. This build implements contract version `0.1.0` and answers only `deadset version`; every other command is reserved and exits with a usage message.

## Quick start

```sh
go install github.com/cplieger/deadset/cmd/deadset@latest
deadset version
```

Go 1.27 or later is required to install from source. The binary is static and needs no C toolchain.

## Security

`deadset` performs no network request during analysis. It executes only analyzers already installed on the local filesystem, at the path its provider list names, and exits with an error rather than fetching one that is missing. It is report-only: no command edits a source file, and any request for one is refused with exit code 2.

## Dependencies

Standard library only; the module declares no dependency. The analyzers it runs are separate programs with their own releases.

## Contributing

Issues and pull requests are welcome. The general guidelines live in [cplieger/.github](https://github.com/cplieger/.github/blob/main/CONTRIBUTING.md).

## Disclaimer

This project is built with care and follows security best practices, but it is intended for personal / self-hosted use. No guarantees of fitness for production environments. Use at your own risk.

This project was built with AI-assisted tooling using [Claude](https://claude.com), [GPT](https://openai.com), and [Kiro](https://kiro.dev). The human maintainer defines architecture, supervises implementation, and makes all final decisions.

## License

GPL-3.0-or-later. See [LICENSE](LICENSE).
