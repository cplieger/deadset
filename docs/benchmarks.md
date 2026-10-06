# Benchmarks

This page is for anyone weighing how far to trust deadset's findings. It gives deadset's measured precision and misses at each confidence level, beside the tools these projects already run. It also gives the method behind every number.

## What was measured

The numbers are for deadset v1.9.0 with deadset-go v1.24.0 and deadset-ts v5.2.0, measured in October 2026. Later releases were not measured. Three sets of projects were analyzed:

- The author's own repositories: 52 repositories with 48 Go modules and 12 TypeScript packages.
- Sixteen open-source projects, listed below, each at one fixed commit.
- Code that a later commit removed: 67 commits in 22 of the author's repositories. Each was analyzed at the commit before the removal, and every removed declaration that nothing read is a known dead item. This set measures misses only.

| Project | Language | Commit |
| --- | --- | --- |
| [spf13/cobra](https://github.com/spf13/cobra) | Go | `adbc881` |
| [fsnotify/fsnotify](https://github.com/fsnotify/fsnotify) | Go | `20b1e15` |
| [mattn/go-sqlite3](https://github.com/mattn/go-sqlite3) | Go | `dbbe931` |
| [prometheus/client_golang](https://github.com/prometheus/client_golang) | Go | `ba782fc` |
| [miniflux/v2](https://github.com/miniflux/v2) | Go | `703fe82` |
| [jesseduffield/lazygit](https://github.com/jesseduffield/lazygit) | Go | `ff375b1` |
| [webpro-nl/knip](https://github.com/webpro-nl/knip) | TypeScript | `99cac78` |
| [TanStack/query](https://github.com/TanStack/query) | TypeScript | `29859ae` |
| [vitest-dev/vitest](https://github.com/vitest-dev/vitest) | TypeScript | `964e1a4` |
| [honojs/hono](https://github.com/honojs/hono) | TypeScript | `f23b146` |
| [sindresorhus/ky](https://github.com/sindresorhus/ky) | TypeScript | `0d59458` |
| [umami-software/umami](https://github.com/umami-software/umami) | TypeScript | `ec0ff50` |
| [usememos/memos](https://github.com/usememos/memos) | Go and TypeScript | `698460d` |
| [woodpecker-ci/woodpecker](https://github.com/woodpecker-ci/woodpecker) | Go and TypeScript | `85ec41a` |
| [owncast/owncast](https://github.com/owncast/owncast) | Go and TypeScript | `21a6f56` |
| [screego/server](https://github.com/screego/server) | Go and TypeScript | `4ae5f44` |

Each project got a `deadset.json` that names its target kind and nothing else. Every ignore list was removed before any tool ran: `.punused-ignore`, `deadset-ignore.json` and `deadset-baseline.json` files, and every knip key whose name starts with `ignore`. Inline directives such as `//nolint`, `// eslint-disable` and `// deadset:ignore` stayed in the source, and so did each project's `.golangci.yaml`.

Findings were judged by reading the code. A finding is correct when the code can be deleted or narrowed with no change in behavior. It is false when something uses the code, for example a caller, reflection, generated code, a build tag or a caller of a published API. It is not decidable when only runtime data could tell.

A finding that several tools agree on and that no rule decides counts as correct. That applies to 253 findings at the default and 256 at `possible`. Where one cause decides many findings, examples were opened in the code and the rest were checked against the same rule.

Some findings were left out of every count:

- Findings in test fixtures, which are sample inputs that tests keep on purpose. That is 293 punused findings in the author's repositories. In the open-source projects it is 106 punused, 800 knip, 48 ESLint, 52 tsc and 726 deadset findings.
- Findings in generated files, because deadset leaves generated files out by default. That is 71 punused findings in the author's repositories and 3,054 in the open-source projects.
- unparam's reports that a result is always nil or that a parameter always receives one value, which deadset has no check for: 7 findings.
- 97 findings of the other tools in the open-source projects that were never judged. deadset also reports one of them.

The deadset-go analyzer gave no answer on one of the author's modules and on 9 commits of the removed-code set. Their tests need a build tag set in the configuration. The deadset-ts analyzer gave no answer on its own repository, whose test fixtures hold a malformed inline directive on purpose. Known dead code in those runs counts as missed.

### Reading the tables

- Findings counts each reported position once, even when a tool reports it twice or, in the combined row, several tools report it.
- Not judged counts findings that were not given a verdict.
- Misses counts the known dead code that a row does not report. Known dead code is every finding that any tool in the table reports and that was judged correct. In the removed-code tables, it is the removed items.
- Precision is correct findings divided by correct and false findings together. Findings that are not decidable are left out of both. Where findings were not judged, the range counts them all false, then all correct.
- Every deadset row comes from one run at `possible`, with each finding counted at its own level. A separate run at the default reported the same findings. Two of them, in the open-source TypeScript, were judged correct there and false here.

### Limits of the measurement

- Known dead code comes from the tools being compared. Dead code that no tool reported is in no count, so every row's misses are a lower bound, deadset's included.
- A miss of deadset at the default can be a finding deadset reports at `possible`. The default hides those findings, so they count as missed in the default row.
- The removed-code set is the one set that does not depend on the tools. Its known dead items are the declarations a later commit deleted.
- Each verdict was reached by reading the code against the rule above. Findings that reading could not settle are counted as not decidable and left out of precision.
- The author's repositories and the open-source projects were also used while developing deadset. Projects it was not tuned on may score lower, and their results will be added once measured.

## The other tools

Each tool ran with the options a CI job gives it.

| Tool | Version | What it reports |
| --- | --- | --- |
| [punused](https://github.com/bep/punused) | v0.5.1 | Unused exported Go symbols, using gopls. It reads references inside the project only |
| [deadcode](https://pkg.go.dev/golang.org/x/tools/cmd/deadcode) | golang.org/x/tools v0.51.0, with `-test` | Go functions and methods that no `main` package or test reaches |
| [golangci-lint](https://golangci-lint.run) `unused` | v2.14.0 | Unused unexported Go constants, variables, functions, types and fields |
| golangci-lint `unparam` | v2.14.0 | Unused Go function parameters and results |
| golangci-lint `wastedassign`, `ineffassign` | v2.14.0 | Go assignments whose value is never read |
| [knip](https://knip.dev) | 6.38.0 in the author's repositories, each project's own version in the open-source ones | Unused files, exports, class members and dependencies |
| [ESLint](https://eslint.org) `@typescript-eslint/no-unused-vars` | ESLint 10.11.0 in the author's repositories, 10.12.0 in the open-source ones, typescript-eslint 8.71.0 | Unused variables and parameters |
| tsc with `noUnusedLocals` and `noUnusedParameters` | Each project's own TypeScript | Unused locals and parameters |

knip ran only where a project has a knip configuration, as its CI does. A CI job runs deadcode only on the 24 of the author's Go modules that hold a `main` package, and here it ran on all 48. On the author's TypeScript, ESLint and tsc reported nothing, and so did `wastedassign` and `ineffassign` on the author's Go.

A tool's misses include kinds of dead code it does not check, and projects where it did not run. [Each tool on its own question](#each-tool-on-its-own-question) compares deadset with each tool on that tool's kinds only.

## Results on the author's repositories

Go, 1,720 known dead items:

| Tool | Findings | Correct | False positives | Not decidable | Not judged | Misses | Precision |
| --- | --- | --- | --- | --- | --- | --- | --- |
| punused | 1,248 | 128 | 726 | 394 | 0 | 1,566 | 15% |
| deadcode | 19 | 15 | 3 | 1 | 0 | 1,705 | 83% |
| `unused` | 9 | 9 | 0 | 0 | 0 | 1,711 | 100% |
| `unparam` | 5 | 5 | 0 | 0 | 0 | 1,715 | 100% |
| Other tools combined | 1,271 | 147 | 729 | 395 | 0 | 1,547 | 17% |
| deadset at `certain` | 1,868 | 1,684 | 178 | 6 | 0 | 36 | 90% |
| deadset at `probable`, the default | 1,868 | 1,684 | 178 | 6 | 0 | 36 | 90% |
| deadset at `possible` | 3,580 | 1,717 | 1,463 | 399 | 1 | 5 | 54% |

TypeScript, 992 known dead items:

| Tool | Findings | Correct | False positives | Not decidable | Not judged | Misses | Precision |
| --- | --- | --- | --- | --- | --- | --- | --- |
| knip | 288 | 278 | 10 | 0 | 0 | 714 | 97% |
| Other tools combined | 288 | 278 | 10 | 0 | 0 | 714 | 97% |
| deadset at `certain` | 1,168 | 804 | 302 | 1 | 61 | 177 | 69% to 74% |
| deadset at `probable`, the default | 1,168 | 804 | 302 | 1 | 61 | 177 | 69% to 74% |
| deadset at `possible` | 1,856 | 981 | 617 | 194 | 64 | 0 | 59% to 63% |

The 177 TypeScript misses at the default are exports of one file that only tests import. A code generator writes it, but its header has neither `// Code generated ... DO NOT EDIT.` nor `@generated`, so it counts. At `possible`, deadset reports them as code only tests use.

## Results on open-source projects

Go, 1,195 known dead items:

| Tool | Findings | Correct | False positives | Not decidable | Not judged | Misses | Precision |
| --- | --- | --- | --- | --- | --- | --- | --- |
| punused | 2,205 | 381 | 1,331 | 493 | 0 | 790 | 22% |
| deadcode | 206 | 74 | 113 | 5 | 14 | 1,120 | 37% to 44% |
| `unused` | 10 | 9 | 0 | 0 | 1 | 1,186 | 90% to 100% |
| `unparam` | 8 | 7 | 1 | 0 | 0 | 1,188 | 88% |
| `wastedassign` | 8 | 8 | 0 | 0 | 0 | 1,187 | 100% |
| `ineffassign` | 3 | 3 | 0 | 0 | 0 | 1,192 | 100% |
| Other tools combined | 2,382 | 431 | 1,442 | 494 | 15 | 740 | 23% to 24% |
| deadset at `certain` | 1,551 | 1,134 | 294 | 81 | 42 | 61 | 77% to 80% |
| deadset at `probable`, the default | 1,551 | 1,134 | 294 | 81 | 42 | 61 | 77% to 80% |
| deadset at `possible` | 1,825 | 1,160 | 516 | 107 | 42 | 36 | 68% to 70% |

TypeScript, 984 known dead items:

| Tool | Findings | Correct | False positives | Not decidable | Not judged | Misses | Precision |
| --- | --- | --- | --- | --- | --- | --- | --- |
| knip | 684 | 124 | 312 | 0 | 248 | 860 | 18% to 54% |
| ESLint | 511 | 122 | 339 | 0 | 50 | 862 | 24% to 34% |
| tsc | 255 | 55 | 184 | 0 | 16 | 929 | 22% to 28% |
| Other tools combined | 1,450 | 301 | 835 | 0 | 314 | 683 | 21% to 42% |
| deadset at `certain` | 2,800 | 774 | 1,585 | 0 | 441 | 208 | 28% to 43% |
| deadset at `probable`, the default | 2,800 | 774 | 1,585 | 0 | 441 | 208 | 28% to 43% |
| deadset at `possible` | 4,708 | 774 | 3,493 | 0 | 441 | 208 | 16% to 26% |

punused reads references inside the project only, as its documentation says. So on a library, its findings about API that other projects call count as false here.

## Results on removed code

The other tools find 53 removed Go items that deadset misses at the default, and 29 that it misses at `possible`. Of the items no other tool reports, deadset finds 53 at the default and 61 at `possible`. In TypeScript, the other tools find one item that deadset misses.

Go, 294 removed items:

| Tool | Items found | Misses |
| --- | --- | --- |
| punused | 197 | 97 |
| deadcode | 16 | 278 |
| `unused` | 10 | 284 |
| `unparam` | 1 | 293 |
| `wastedassign` | 0 | 294 |
| `ineffassign` | 1 | 293 |
| Other tools combined | 212 | 82 |
| deadset at `certain` | 212 | 82 |
| deadset at `probable`, the default | 212 | 82 |
| deadset at `possible` | 244 | 50 |

TypeScript, 71 removed items:

| Tool | Items found | Misses |
| --- | --- | --- |
| knip | 4 | 67 |
| ESLint | 0 | 71 |
| tsc | 1 | 70 |
| Other tools combined | 5 | 66 |
| deadset at every level | 62 | 9 |

Removed code that only tests used is left out: 16 Go items and 14 TypeScript items. Where a commit's project declares entry points or consumers, deadset ran with them.

## Accuracy of each confidence level

The table splits the findings by level, so a row is the findings of that level alone.

| Projects | Language | Level | Findings | Correct | False positives | Not decidable | Not judged | Precision |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| Author's repositories | Go | `certain` | 1,868 | 1,684 | 178 | 6 | 0 | 90% |
| Author's repositories | Go | `possible` | 1,712 | 33 | 1,285 | 393 | 1 | 3% |
| Author's repositories | TypeScript | `certain` | 1,168 | 804 | 302 | 1 | 61 | 69% to 74% |
| Author's repositories | TypeScript | `possible` | 688 | 177 | 315 | 193 | 3 | 36% |
| Open-source projects | Go | `certain` | 1,551 | 1,134 | 294 | 81 | 42 | 77% to 80% |
| Open-source projects | Go | `possible` | 274 | 26 | 222 | 26 | 0 | 10% |
| Open-source projects | TypeScript | `certain` | 2,800 | 774 | 1,585 | 0 | 441 | 28% to 43% |
| Open-source projects | TypeScript | `possible` | 1,908 | 0 | 1,908 | 0 | 0 | 0% |

No finding was `probable`. A finding is `probable` only when a library declares its consumers and some of them do not load. No project here was set up that way. So the default reported the same findings as `certain`.

The `possible` findings are a library's exported API that has no declared consumer, code that only tests use, and the tests of that API. Other projects may call that API. At `possible`, deadset also reported 808 findings on test helpers that tests use, as code only tests use. These were not judged. They are 118 and 275 in the author's Go and TypeScript, and 162 and 253 in the open-source Go and TypeScript.

On removed code, the `certain` findings found 212 Go items and 62 TypeScript items. The `possible` findings found 32 more Go items, 24 of them library exports, and no more TypeScript items.

## Each tool on its own question

This table keeps only the kinds of dead code each tool reports, on the projects where it reported anything. It counts how many of the tool's correct findings deadset also reports, at the default and at `possible`. The second count is scored on the `possible` run, so a tool's total can differ by one.

| Tool | Projects | deadset at the default | deadset at `possible` |
| --- | --- | --- | --- |
| punused | Author's repositories | 128 of 128 | 129 of 129 |
| deadcode | Author's repositories | 10 of 15 | 10 of 15 |
| `unused` | Author's repositories | 9 of 9 | 9 of 9 |
| `unparam` | Author's repositories | 5 of 5 | 5 of 5 |
| knip | Author's repositories | 101 of 278 | 278 of 278 |
| punused | Open-source projects | 364 of 381 | 363 of 380 |
| deadcode | Open-source projects | 59 of 74 | 59 of 74 |
| `unused` | Open-source projects | 9 of 9 | 9 of 9 |
| `unparam` | Open-source projects | 6 of 7 | 6 of 7 |
| `wastedassign` | Open-source projects | 5 of 8 | 5 of 8 |
| `ineffassign` | Open-source projects | 3 of 3 | 3 of 3 |
| knip | Open-source projects | 88 of 124 | 88 of 124 |
| ESLint | Open-source projects | 3 of 122 | 3 of 122 |
| tsc | Open-source projects | 2 of 55 | 2 of 55 |

The 177 knip findings that deadset reports only at `possible` are the exports of that one file, which only tests import.

## Known gaps

deadset does not yet report these kinds of dead code that another tool reports:

- An unused trailing parameter of a TypeScript callback, such as `catch (_error) {}` or `(id, _sources) =>`. ESLint reports 61 in the open-source projects.
- Unused locals and parameters inside TypeScript test files. ESLint reports 58 and tsc 53 in the open-source projects.
- A devDependency that nothing imports or runs. knip reports 36 in the open-source projects.
- Methods of a Go type that only dead code constructs, or of a test fake whose methods no test calls. An interface the type satisfies keeps them. The deadcode tool reports 13 in the open-source projects and 5 in the author's. Four removed items are of this kind.
- Fields that a Go decoder fills and nothing reads, and test-only methods of a decoded type. The punused tool reports 16 in the open-source projects. Five removed items are of this kind.
- The methods that implement a Go interface method nothing calls. The interface method is reported with a list of them, but they are not findings of their own. That covers 10 removed items.
- A declaration in a test file that nothing references: 7 removed Go items, and 2 deadcode findings in the open-source projects.
- A Go assignment whose value is never read, in 3 places `wastedassign` reports.
- An unused parameter of a Go closure, which `unparam` reports once.
- A TypeScript export in a file that a tool's configuration names, such as a Stryker `mutate` list, which counts as used: 1 removed item.

deadset reports live code as dead most often in these cases:

- Members of a TypeScript type that a library's published API exposes, and members of a class marked `@public`: 539 false positives in the open-source projects.
- Members of files that an HTML page or a configuration loads. They are often used through a path deadset does not follow. That gives 491 false positives and 201 findings not judged in the open-source TypeScript.
- A member of a TypeScript type selected by a literal key, such as `T["name"]` or `on("event", handler)`. That gives 150 false positives in the author's repositories and 64 in the open-source projects.
- Example applications, scripts a CI workflow runs, Vue template reads and `declare module` blocks: 174 false positives in the open-source TypeScript.
- Exports of a package whose manifest points at build output, such as `./dist/devtools/index.mjs` or a VS Code extension's `./dist/extension.js`. deadset does not map these back to their source: 109 false positives in the open-source TypeScript.
- A Go value passed as `any` to a registration function in another module, whose methods and everything they call look unused: 46 false positives.
