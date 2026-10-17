# Libraries and their consumers

This page is for anyone checking a library whose exported API other repositories call. It covers the scope document, how deadset hands each consumer to an analyzer, and what the findings then mean.

## The scope document

A library's exported API has callers outside the library. A finding about an exported symbol is only as certain as the set of callers the analysis loaded. `--scope` names a scope document that lists the target and its consumers, each a directory on the local filesystem.

```sh
deadset analyze --target=lib --scope=scope.json
```

```json
{
  "target": { "path": "lib" },
  "consumers": [{ "path": "app" }, { "path": "web" }]
}
```

The document is the scope document [deadset-spec](https://github.com/cplieger/deadset-spec/tree/v8.0.0) publishes in [`contract/scope.schema.json`](https://github.com/cplieger/deadset-spec/blob/v8.0.0/contract/scope.schema.json), the same one each analyzer's own `--scope` reads. A relative path is resolved against the directory that holds the document. Its target must be the directory `--target` names, or the run exits with 2. An optional `workspace` member names the `go.work` file through which the consumers resolve the target.

## How consumers reach the analyzers

- Each consumer is handed to every analyzer claiming a language detected in it, and to no other. A Go consumer reaches the Go analyzer and a TypeScript one the TypeScript analyzer.
- A consumer holding a language no analyzer of the run claims is refused with exit code 2.
- A consumer that does not exist, is not a directory or holds no language does not load, and the run ends with exit code 3 and the line `setup failure: missing-consumer`. A scope document that does not meet the schema, or cannot be read, ends the run with exit code 2.
- The analyzers run in the deepest directory holding the target and every consumer. The merged report's `target.root`, and each consumer's path under `consumers.loaded`, are relative to that directory.
- The run directory keeps the declared scope as `scope.json`. An analyzer handed only some of the consumers reads its own, `scope.<name>.json`.

When the scope document gives a target or consumer no `id`, deadset names it in the scope it hands every analyzer: by its Go module path when the run loads it as Go, else by the `name` its `package.json` gives it. A target both languages load that neither names, its Go modules in directories below it, is named `.`. Every report of a module then names it alike, and the merge reads the two analyzers' reports as one module.

## What the findings mean

With every declared consumer loaded, a library's findings are `certain`. With no scope document the target is analyzed alone, and a finding about a library's published API is `possible`. The default minimum confidence, `probable`, leaves such a finding out, and `--min-confidence=possible` reports it. [Benchmarks](benchmarks.md#accuracy-of-each-confidence-level) gives the measured accuracy of each level. When no `go.mod` governs the target, every Go module below it is part of the target, except one inside a directory the Go analyzer skips, `vendor`, `testdata` and one whose name starts with `.` or `_` among them. A target with a `go.mod` at its root is that module, with the modules below it that its `go.work` lists. A Go module outside the target is a consumer only when the scope document declares it.

## Putting the consumers in place

deadset never clones, fetches or checks out a consumer. Whatever runs it puts every consumer on the filesystem first. In CI, check each one out before the step that runs deadset. In a container, mount each one and give deadset a scope document naming the mounted paths.
