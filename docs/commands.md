# Commands and configuration

This page lists what deadset reads, what it runs and what it writes. It is for anyone wiring deadset into CI or a script.

## Commands

| Command | What it does |
| --- | --- |
| `deadset analyze` | Runs every analyzer a language in scope needs, merges their reports, prints the result and exits with the verdict |
| `deadset print-config` | Prints the resolved configuration as JSON and the source of every setting |
| `deadset version` | Prints the deadset version and the contract version it implements, one to a line |

Each command takes only the options listed for it, and `version` takes none. Any other option, a `--fix` flag among them, and any argument end the run with exit code 2 and print the command's usage text. deadset only reports and never edits a source file.

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

`print-config` takes `--target`, `--central` and the last four flags. A flag value outside its set ends the run with exit code 2. So do a `--central`, `--scope` or `--template` file that cannot be read, and a `--scope` document the scope schema refuses. A `--run-dir` that exists, or whose parent does not, ends the run with exit code 3 and `run failure: unwritten-output`.

## Configuration

deadset reads each setting from the first of four sources that sets it:

1. The flags on the command line.
2. `deadset.json` at the target root.
3. The central configuration `--central` names.
4. The setting's default.

The keys are those of the contract's [configuration schema](https://github.com/cplieger/deadset-spec/blob/v8.0.0/contract/config.schema.json). A run exits with 2 on a key the schema does not declare, a value it refuses, or a document over 1 MiB. The table below names the settings deadset acts on itself. It checks every other setting and hands it to the analyzers, whose own docs describe them.

| Key | Default | Description |
| --- | --- | --- |
| `target.kind` | required | `application` or `library` for the whole target, or an object naming the kind of each language, as `{"go": "application", "ts": "library"}`, with both members. It is never inferred, and a run with no source setting it exits with 2. Where the languages' kinds differ, the merged report's kind is `mixed` and its `language_kinds` names each |
| `analysis.languages` | `[]` | The languages in scope, `go` or `ts`. Empty detects them from the tree |
| `providers.analyzers` | `deadset-go` for `go`, `deadset-ts` for `ts` | The analyzers a run may invoke, described below |
| `reporters.formats` | `["text"]` | The formats, at least one |
| `reporters.fail_on` | `deny` | The lowest severity that fails the run |
| `reporters.sort` | `position` | `position`, or `size` to list the largest deletion first |
| `reporters.max_findings` | `0` | The most findings the report lists. `0` lists every one, and the exit code always counts every finding |

## Documents at the target root

Before any analyzer runs, deadset checks the documents every analyzer reads at the target root: the ignore file `deadset-ignore.json`, the baseline `deadset-baseline.json` and the edges document `deadset-edges.json`. A run exits with 2 on one that is not strict JSON, that the contract refuses or that is larger than 1 MiB, every entry checked whatever its language, and names the document and the entry. An absent document is none. A document that cannot be read ends the run with exit code 3 and `setup failure: unreadable-document`, as does one that is not a regular file inside the target root, such as a symbolic link to a file outside it or a named pipe. Whether a symbol reference is valid in its language stays the check of that language's analyzer.

## How languages are detected

With `analysis.languages` empty, deadset reads the names of the files under the target, never their contents. Go is in scope when the tree holds a `go.mod` or a `.go` file. TypeScript is in scope when it holds a `tsconfig*.json`, the one file its analyzer reads a program from. Each language skips the directories its analyzer skips. Go skips `node_modules`, `testdata`, `vendor` and every directory whose name starts with a full stop or an underscore. TypeScript skips only `node_modules` and `.git`, so a `tsconfig.json` under `.config` or `testdata` puts it in scope. A tree with no language in scope ends the run with exit code 3 and the line `setup failure: no-program`.

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
- An entry whose command finds no file, or does not start, ends the run with exit code 3 and `run failure: missing-analyzer`. deadset never downloads an analyzer.
- Before any analysis, deadset runs each analyzer's `describe` command. It admits an analyzer that names itself as its entry does, claims every language the run hands it, and reads a report schema version deadset accepts. The analyzer must also record a pass of the contract's conformance corpus. Any other ends the run with exit code 3 and `run failure: unadmitted-analyzer`.
- An analyzer that exits with 2 has refused its own invocation or the configuration it was given. The run exits with 2 and names that analyzer after the lines it printed. deadset does not print its own usage text then, since its command line was not at fault.

Each analyzer runs as a separate process, in the deepest directory that holds the target and every consumer. The analyzers are separate programs with their own releases. deadset parses no source itself. Every finding comes from an analyzer's report, except `DS9005`, the stale edge the merge reports. Each analyzer is handed every configured root, so one analyzer can report a root unmatched (`DS9004`) that another matched. The merge drops every `DS9004` finding for a root that an analyzer reading it matched: an analyzer of the language the root's prefix names, or any analyzer for a root with no language prefix. deadset makes no network request.

## Outputs

Standard output carries the `text` and `github` formats, in the order `--formats` lists them. The `text` format ends with a line that counts the findings `--min-confidence` left out, when it left any out. Next come one line per analyzer that ran, with its version and the sha256 of its binary, and the summary line. Last come the path of each format written as a file, and a remediation line when a finding fails the run.

Standard error names the run directory, the count of stale suppressions when there are any, and the verdict when `--exit-code=off` hid it. What each analysis printed follows the run directory line, in analyzer name order, without its failure lines. Each line an analyzer printed, from its `describe` verb or its analysis, opens with the analyzer's name, a colon and a space, as in `deadset-go: <line>`, so no analyzer line can open with a failure prefix.

A run that ends with exit code 3 then prints exactly one class line. It opens with `setup failure: <class>:` for a cause in the project, or with `run failure: <class>:` for a cause in the environment or an analyzer, and names what failed and the fix. Each further failure follows on its own line as `<class>: <detail>`, with no prefix. When analyzers fail, the class line is that of the first failing analyzer in the provider list: the class line it printed when it ended with 3, or else deadset's own failure for it. Every other failure line the analyzers that ended with 3 printed follows without its prefix. An analyzer that ends with 3 and prints no class line, and any error deadset has no class for, is `run failure: product-fault`. An interrupt or a termination signal stops every analyzer and ends the run with `run failure: interrupted-run`.

The `sarif` format writes SARIF 2.1.0. It reads the source lines its results name, inside the target root only. The `template` format renders the template `--template` names. The template uses the subset of Go's `text/template` that the contract's [template rendering](https://github.com/cplieger/deadset-spec/blob/v8.0.0/contract/grammar/template.md) states. It reads the merged report by its JSON member names. A run that asks for `template` with no `--template` ends with exit code 2 before any analyzer runs. So does one that names a template that cannot be read or does not parse.

## The run directory

A run directory belongs to one run, and no file in it is ever overwritten. It holds:

| File | Contents |
| --- | --- |
| `scope.json` | The scope the run declared, with every path absolute |
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
| 2 | The run was asked something it refuses: a malformed command line or configuration, no target kind, a `fix` flag, or a configuration, scope or template file that cannot be read |
| 3 | The run produced no answer, and standard error names the class: the tree holds no program, a consumer did not load or a document at the target root cannot be read (`setup failure:`), or an analyzer was missing, not admitted or failed, a cross-language finding had no analyzer report its paired side, the run directory, merged report or a rendering could not be written, or the run was interrupted (`run failure:`) |
| 4 | Reserved by the contract for a report that still holds a pending finding. deadset settles every cross-language pair in the merge or ends the run with 3, so `analyze` does not return 4 |

A stale suppression fails the run whatever its severity. A run that ends with 2 or 3 keeps that code under `--exit-code=off`. When one analyzer stops on a setup failure, the run still ends with 3. After that analyzer's lines, standard error names the `analysis.languages` value for the other languages and the analyzer you can run alone instead. The contract's [exit codes](https://github.com/cplieger/deadset-spec/blob/v8.0.0/docs/exit-codes.md) page states the whole table.

## Versions

`deadset version` prints the version of the build and the contract version it implements, 8.0.0. The test suite checks the binary against the contract files of [deadset-spec](https://github.com/cplieger/deadset-spec/tree/v8.0.0) and uses [rapid](https://pkg.go.dev/pgregory.net/rapid) for property tests. Neither is part of the binary.
