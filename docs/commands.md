# Commands and configuration

This page lists what deadset reads, what it runs and what it writes. It is for anyone wiring deadset into CI or a script.

## Commands

| Command | What it does |
| --- | --- |
| `deadset analyze` | Runs every analyzer a language in scope needs, merges their reports, prints the result and exits with the verdict |
| `deadset print-config` | Prints the resolved configuration as JSON and the source of every setting |
| `deadset version` | Prints the deadset version and the contract version it implements, one to a line |
| `explain`, `install`, `describe` | Reserved. Each prints the usage text and exits with 2 in this build |

Every command exits with 2 when its command line holds a `fix` flag in any spelling. `--fix` and `-fix=true` are two such spellings. deadset only reports and never edits a source file.

## Flags of analyze

| Flag | Description | Default |
| --- | --- | --- |
| `--target` | The target root, which holds `deadset.json` | `.` |
| `--scope` | The scope document naming the target and its consumers, described in [Libraries and their consumers](scope.md) | _(unset)_ |
| `--central` | A central configuration, whose settings `deadset.json` overrides | _(unset)_ |
| `--run-dir` | The run directory, which must not exist yet and whose parent must | a new directory under the temporary directory |
| `--template` | The template file the `template` format renders | _(unset)_ |
| `--exit-code` | `on`, or `off` to exit with 0 whatever the verdict | `on` |
| `--languages` | The languages in scope, `go` and `ts`, separated by commas | detected from the tree |
| `--min-confidence` | The lowest class reported: `certain`, `probable` or `possible` | `probable` |
| `--formats` | The formats, separated by commas: `text`, `json`, `github`, `sarif`, `template` | `text` |
| `--fail-on` | The lowest severity that fails the run: `allow`, `warn` or `deny` | `deny` |

`print-config` takes `--target`, `--central` and the last four flags. A flag value outside its set ends the run with exit code 2. So do a `--run-dir` that exists and a `--central` file that does not.

## Configuration

deadset reads each setting from the first of four sources that sets it:

1. The flags on the command line.
2. `deadset.json` at the target root.
3. The central configuration `--central` names.
4. The setting's default.

The keys are those of the contract's [configuration schema](https://github.com/cplieger/deadset-spec/blob/v6.0.2/contract/config.schema.json). A run exits with 2 on a key the schema does not declare, a value it refuses, or a document over 1 MiB. The table below names the settings deadset acts on itself. It checks every other setting and hands it to the analyzers, whose own docs describe them.

| Key | Default | Description |
| --- | --- | --- |
| `target.kind` | required | `application` or `library`. It is never inferred, and a run with no source setting it exits with 2 |
| `analysis.languages` | `[]` | The languages in scope, `go` or `ts`. Empty detects them from the tree |
| `providers.analyzers` | `deadset-go` for `go`, `deadset-ts` for `ts` | The analyzers a run may invoke, described below |
| `reporters.formats` | `["text"]` | The formats, at least one |
| `reporters.fail_on` | `deny` | The lowest severity that fails the run |
| `reporters.sort` | `position` | `position`, or `size` to list the largest deletion first |
| `reporters.max_findings` | `0` | The most findings the report lists. `0` lists every one, and the exit code always counts every finding |

## How languages are detected

With `analysis.languages` empty, deadset reads the names of the files under the target, never their contents. Go is in scope when the tree holds a `go.mod` or a `.go` file. TypeScript is in scope when it holds a `tsconfig*.json`, or a `.ts`, `.tsx`, `.mts` or `.cts` file together with a `package.json` anywhere in the tree. Directories named `node_modules`, `testdata` or `vendor`, and every directory whose name starts with a full stop, are skipped. A tree with no language in scope ends the run with exit code 2.

## The provider list

`providers.analyzers` lists the analyzers a run may invoke. The default is:

```json
[
  { "name": "deadset-go", "languages": ["go"], "command": "deadset-go" },
  { "name": "deadset-ts", "languages": ["ts"], "command": "deadset-ts" }
]
```

- A command is a name deadset looks up on `PATH`, or an absolute path. A relative path is refused.
- A run invokes every entry that claims a language in scope, two entries for one language included. A language in scope that no entry claims ends the run with exit code 2.
- An entry whose command finds no file ends the run with exit code 3. deadset never downloads an analyzer.
- Before any analysis, deadset runs each analyzer's `describe` command. It admits an analyzer that names itself as its entry does and reads a report schema version deadset accepts. The analyzer must also record a pass of the contract's conformance corpus. Any other ends the run with exit code 3.

Each analyzer runs as a separate process, in the deepest directory that holds the target and every consumer. The analyzers are separate programs with their own releases. deadset parses no source itself. Every finding comes from an analyzer's report, except `DS1705`, the stale edge the merge reports. deadset makes no network request.

## Outputs

Standard output carries the `text` and `github` formats, in the order `--formats` lists them. The `text` format ends with a line that counts the findings `--min-confidence` left out, when it left any out. Next come one line per analyzer that ran, with its version and the sha256 of its binary, and the summary line. Last come the path of each format written as a file, and a remediation line when a finding fails the run.

Standard error names the run directory, the count of stale suppressions when there are any, and the verdict when `--exit-code=off` hid it. What each analysis printed follows the run directory line, whole and in analyzer name order. An analyzer's `setup failure:` and `memory exhausted:` lines appear there as the analyzer wrote them.

The `sarif` format writes SARIF 2.1.0. It reads the source lines its results name, inside the target root only. The `template` format renders the template `--template` names. The template uses the subset of Go's `text/template` that the contract's [template rendering](https://github.com/cplieger/deadset-spec/blob/v6.0.2/contract/grammar/template.md) states. It reads the merged report by its JSON member names. A run that asks for `template` with no `--template` ends with exit code 2 before any analyzer runs. So does one that names a template that cannot be read or does not parse.

## The run directory

A run directory belongs to one run, and no file in it is ever overwritten. It holds:

| File | Contents |
| --- | --- |
| `scope.json` | The scope the run declared, with every path relative to the directory the analyzers run in |
| `describe.<analyzer>.json` | What the analyzer printed for `describe` |
| `config.<analyzer>.json` | The configuration the analyzer was handed |
| `scope.<analyzer>.json` | The scope of an analyzer handed only some of the consumers |
| `report.<analyzer>.json` | The analyzer's report |
| `report.json` | The merged report, which is the `json` format |
| `report.json.sarif` | The `sarif` format, when requested |
| `report.json.tmpl` | The `template` format, when requested |

`<analyzer>` is the entry's name in the provider list, so two entries for one language keep separate files.

## Exit codes

| Code | Meaning |
| --- | --- |
| 0 | No finding fails the run, and the report holds no stale suppression. Also the code of a run with `--exit-code=off` that produced a report, whatever its verdict |
| 1 | A finding at or above `reporters.fail_on`, or a stale suppression whatever its severity |
| 2 | The run was asked something it refuses: a malformed command line or configuration, no target kind, a `fix` flag or an unreadable template |
| 3 | The run produced no answer: an analyzer was missing, refused or failed, a consumer did not load, or a cross-language finding had no analyzer report its paired side |
| 4 | Reserved by the contract for a report that still holds a pending finding. deadset settles every cross-language pair in the merge or ends the run with 3, so `analyze` does not return 4 |

A stale suppression fails the run whatever its severity. A run that ends with 2 or 3 keeps that code under `--exit-code=off`. When one analyzer stops on a setup failure, the run still ends with 3. After that analyzer's lines, standard error names the `analysis.languages` value for the other languages and the analyzer you can run alone instead. The contract's [exit codes](https://github.com/cplieger/deadset-spec/blob/v6.0.2/docs/exit-codes.md) page states the whole table.

## Versions

`deadset version` prints the version of the build and the contract version it implements, 6.0.0. The test suite checks the binary against the contract files of [deadset-spec](https://github.com/cplieger/deadset-spec/tree/v6.0.2) and uses [rapid](https://pkg.go.dev/pgregory.net/rapid) for property tests. Neither is part of the binary.
