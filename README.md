# deadset

[![Go Reference](https://pkg.go.dev/badge/github.com/cplieger/deadset.svg)](https://pkg.go.dev/github.com/cplieger/deadset) [![Go version](https://img.shields.io/github/go-mod/go-version/cplieger/deadset)](https://github.com/cplieger/deadset/blob/main/go.mod) [![Mutation](https://img.shields.io/endpoint?url=https://raw.githubusercontent.com/cplieger/deadset/badges/mutation.json)](https://github.com/cplieger/deadset/issues?q=label%3Agremlins-tracker)

deadset finds dead code in repositories that mix Go and TypeScript, and counts a Go type as used while the TypeScript generated from it is in use.

It runs the [deadset-go](https://github.com/cplieger/deadset-go) and [deadset-ts](https://github.com/cplieger/deadset-ts) analyzers and merges their reports into one result and one exit code. The deadset-go analyzer supports Linux only, and deadset-ts needs Node.js 24 or later. The deadset command uses only the standard library, builds as a static binary with no C toolchain, needs Go 1.27.1 or later and is licensed under GPL-3.0-or-later.

## Why use it

deadset is built for a CI gate on dead code where Go and TypeScript meet, such as a Go server with a generated TypeScript client, or a library and the repositories that use it.

- It detects the languages from file names and runs only the analyzers they need, so a Go-only tree never needs Node.js.
- You pair a Go declaration with its generated TypeScript in `deadset-edges.json`, and deadset reports the pair only when neither side is used.
- It also checks the repositories that use a library, so an export they call is not reported as dead.
- It writes one merged report as text, JSON, GitHub annotations, SARIF 2.1.0 or your own template.
- It only reports, makes no network request and runs only analyzers already installed.

In a repository with one language, deadset-go or deadset-ts alone gives the same findings. deadset is pre-release, so the report shape, exit codes and configuration keys can still change.

## Install

```sh
go install github.com/cplieger/deadset/cmd/deadset@latest
go install github.com/cplieger/deadset-go/cmd/deadset-go@latest
npm install --global @cplieger/deadset-ts
```

## Usage

Install the analyzer for each language your repository holds, so `deadset-go` or `deadset-ts` is on your `PATH`. Then create a `deadset.json` at the root that names the target kind, `application` or `library`, and run `analyze` there:

```sh
echo '{ "target": { "kind": "application" } }' > deadset.json
deadset analyze
```

On a Go module with one uncalled function, the run prints:

```text
main.go:5:6: function unused: unexported function has no reference in the target [certain] (DS1002)
analyzer deadset-go <version> sha256:<digest of the deadset-go binary>
summary: 1 finding (0 allow, 0 warn, 1 deny), 1 deletable line, 0 suppressions in effect, 0 reasons recorded, 0 stale suppressions, 0 pending, 0 omitted, across go
remediation: for each failing finding, delete the symbol, wire it up so the program uses it, or record an adjudication with its reason in an inline directive or a deadset-ignore.json entry
```

It exits with 1, and standard error names the run directory that keeps every report. A run with no target kind exits with 2, and one with an analyzer missing from `PATH` exits with 3.

### Pairing a Go type with its generated TypeScript

A Go type that only its generated TypeScript client uses has no reference in Go. Declare the pair in `deadset-edges.json` at the target root:

```json
{
  "description": "The wire type the server relays, paired with the TypeScript interface generated from it.",
  "edges": [
    {
      "id": "wire/ServerEvent",
      "because": "generated",
      "provides": "go://example.com/server/internal/wire#ServerEvent",
      "used_by": "ts://@example/web/src/wire/types.gen.ts#ServerEvent"
    }
  ]
}
```

deadset reports the pair only when both sides are unused. An edge whose side neither analyzer finds is reported as `DS1705`. [Cross-language edges](https://github.com/cplieger/deadset-spec/blob/v5.3.1/docs/edges.md) describes the format.

### Checking a library against its consumers

List a library and its consumers, each a local directory, in a scope document:

```sh
deadset analyze --target=lib --scope=scope.json
```

```json
{
  "target": { "path": "lib" },
  "consumers": [{ "path": "app" }, { "path": "web" }]
}
```

With every declared consumer loaded, the library's findings are `certain`. With no scope document, exported API findings are hidden by default. Because deadset never fetches a consumer, check each one out first. [Libraries and their consumers](docs/scope.md) gives the rules.

## API

deadset is a command, and its interface is its verbs, the merged JSON report and the exit codes.

- `analyze` runs every analyzer a language in scope needs, merges their reports and exits with the verdict.
- `print-config` prints the resolved configuration with the source of each setting, and `version` prints the deadset and contract versions.
- `explain`, `install` and `describe` are reserved verbs that do nothing yet and exit with 2.
- The run exits 0 when nothing fails it and 1 for a failing finding or a stale suppression. It exits 2 on a usage error or a `--fix` flag, and 3 when it cannot produce an answer.

The report follows version 5.3.0 of the [deadset contract](https://github.com/cplieger/deadset-spec/tree/v5.3.1). [Commands and configuration](docs/commands.md) lists the flags, the configuration sources, the provider list, the output files and the exit codes.

## Measured accuracy

deadset v1.9.0 was measured with deadset-go v1.24.0 and deadset-ts v5.2.0. It ran beside the dead-code tools that Go and TypeScript projects run, on the author's repositories and on 16 open-source projects. Precision is the share of judged findings that are correct. Misses count the known dead code\* that a row does not report.

| Projects | Language | deadset at the default | Other tools combined | `possible` findings alone |
| --- | --- | --- | --- | --- |
| Author's repositories | Go | 90% precision, 36 misses | 17% precision, 1,547 misses | 3% precision |
| Author's repositories | TypeScript | 69% to 74% precision, 177 misses | 97% precision, 714 misses | 36% precision |
| Open-source projects | Go | 77% to 80% precision, 61 misses | 23% to 24% precision, 740 misses | 10% precision |
| Open-source projects | TypeScript | 28% to 43% precision, 208 misses | 21% to 42% precision, 683 misses | 0% precision |

\* Known dead code is every finding that one of the tools reported and that was judged correct by reading the code. Dead code that no tool reported is not counted, so the real misses of every row are higher. The [measurement on removed code](docs/benchmarks.md#results-on-removed-code) uses what a later commit deleted instead.

Every finding at the default was `certain`, so that column is also the measured accuracy of `certain`. `possible` findings are a library's API with no declared consumer, code only tests use, and the tests of that API. The other tools' misses include kinds of dead code they do not check. They also include the open-source projects with no knip configuration, where knip did not run. [Benchmarks](docs/benchmarks.md) gives the full tables, each tool on its own kinds, the method and the known gaps.

## Related projects

deadset implements version 5.3.0 of the [deadset contract](https://github.com/cplieger/deadset-spec/tree/v5.3.1), which defines the issue codes, the report schema, the merge and the exit codes. Another Go or TypeScript analyzer can take the place of deadset-go or deadset-ts. That analyzer must name itself as its provider entry does, read a report schema version deadset accepts and pass the contract's conformance corpus.

- [deadset-go](https://github.com/cplieger/deadset-go) analyzes a Go module and the repositories that import it.
- [deadset-ts](https://github.com/cplieger/deadset-ts) analyzes TypeScript and JavaScript projects, down to class and type members.

## Documentation

- [Commands and configuration](docs/commands.md) lists the flags, the configuration sources, the provider list, the output files and the exit codes, for wiring deadset into CI or a script.
- [Libraries and their consumers](docs/scope.md) explains the scope document and how each consumer reaches an analyzer.
- [Benchmarks](docs/benchmarks.md) gives the measured precision and misses of each confidence level beside other dead-code tools, for judging how far to trust a finding.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).

## Disclaimer

This project is built with care and follows security best practices, but it is intended for personal / self-hosted use. No guarantees of fitness for production environments. Use at your own risk.

This project was built with AI-assisted tooling using [Claude](https://claude.com), [GPT](https://openai.com), and [Kiro](https://kiro.dev). The human maintainer defines architecture, supervises implementation, and makes all final decisions.

## License

GPL-3.0-or-later. See [LICENSE](LICENSE).
