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

The document is the scope document [deadset-spec](https://github.com/cplieger/deadset-spec/tree/v7.0.0) publishes in [`contract/scope.schema.json`](https://github.com/cplieger/deadset-spec/blob/v7.0.0/contract/scope.schema.json), the same one each analyzer's own `--scope` reads. A relative path is resolved against the directory that holds the document. Its target must be the directory `--target` names, or the run exits with 2. An optional `workspace` member names the `go.work` file through which the consumers resolve the target.

## How consumers reach the analyzers

- Each consumer is handed to every analyzer claiming a language detected in it, and to no other. A Go consumer reaches the Go analyzer and a TypeScript one the TypeScript analyzer.
- A consumer holding a language no analyzer of the run claims is refused with exit code 2.
- A consumer that does not exist, is not a directory or holds no language does not load, and the run ends with exit code 3. A scope document that cannot be read or does not meet the schema also ends the run with exit code 3.
- The analyzers run in the deepest directory holding the target and every consumer. The merged report's `target.root`, and each consumer's path under `consumers.loaded`, are relative to that directory.
- The run directory keeps the declared scope as `scope.json`. An analyzer handed only some of the consumers reads its own, `scope.<name>.json`.

A target or consumer can hold both Go and TypeScript. When the scope document gives it no `id`, deadset names it by its Go module path in every report. The merge then reads the two analyzers' reports as one module.

## What the findings mean

With every declared consumer loaded, a library's findings are `certain`. With no scope document the target is analyzed alone, and a finding about a library's published API is `possible`. The default minimum confidence, `probable`, leaves such a finding out, and `--min-confidence=possible` reports it. [Benchmarks](benchmarks.md#accuracy-of-each-confidence-level) gives the measured accuracy of each level. A Go workspace's other modules are consumers only when the scope document declares them.

## Putting the consumers in place

deadset never clones, fetches or checks out a consumer. Whatever runs it puts every consumer on the filesystem first. In CI, check each one out before the step that runs deadset. In a container, mount each one and give deadset a scope document naming the mounted paths.
