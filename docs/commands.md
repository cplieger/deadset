# Commands and configuration

This page lists what deadset reads, what it runs and what it writes. It is for anyone wiring deadset into CI or a script.

## Commands

| Command | What it does |
| --- | --- |
| `deadset analyze` | Runs every analyzer a language in scope needs, merges their reports, prints the result and exits with the verdict |
| `deadset print-config` | Prints the resolved configuration as JSON and the source of every setting |
| `deadset print-languages` | Prints, as JSON, the languages a run puts in scope, the analyzers it runs and the languages it leaves out |
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

`print-config` takes `--target`, `--central` and the last four flags, and `print-languages` takes `--target`, `--scope`, `--central` and `--languages`. A flag value outside its set ends the run with exit code 2. So do a `--central`, `--scope` or `--template` file that cannot be read, and a `--scope` document the scope schema refuses. A `--run-dir` that exists, or whose parent does not, ends the run with exit code 3 and `run failure: unwritten-output`.

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

With `analysis.languages` empty, deadset reads the names of the files under the target, never their contents. Go is in scope when the tree holds a `go.mod`, or a `.go` file whose name starts with neither a full stop nor an underscore, the files the go command reads. TypeScript is in scope when it holds a `tsconfig*.json` or a `jsconfig*.json`, or a JavaScript file (`.js`, `.jsx`, `.mjs` or `.cjs`) whose name does not end with `.min.js`, `.min.cjs` or `.min.mjs`, the files its analyzer reads a program from. A `.ts` file alone does not put TypeScript in scope. Each language skips the directories its analyzer skips. Go skips `node_modules`, `testdata`, `vendor` and every directory whose name starts with a full stop or an underscore. TypeScript skips only `node_modules` and `.git`, so a `tsconfig.json` under `.config` or `testdata` puts it in scope. Protocol Buffers is in scope when the tree holds a `buf.yaml`, a `buf.work.yaml` or a `.proto` file outside `node_modules`, `vendor`, directories whose names start with a full stop, and the test-input directories `testdata`, `fixtures` and `__fixtures__`. Within a `buf.yaml` module, the schema files its `excludes` or `includes` leave out are not read, since `buf` does not build them. A tree whose only schema files are ones deadset does not read, or that holds a `buf.yaml` and no `.proto` file, holds no Protocol Buffers program: deadset then leaves Protocol Buffers out as it leaves out a language whose analyzer finds no program, as described below, and where `analysis.languages` names it the run ends with exit code 3 and `setup failure: no-program`. deadset reads Protocol Buffers itself, so no analyzer is installed for it, and a provider entry that claims it ends the run with exit code 2. With Protocol Buffers in scope, deadset asks every analyzer of the run for the bindings of the code generated from the schemas, so an analyzer that cannot write them ends the run with exit code 3 and the line `run failure: unadmitted-analyzer`. A tree with no language in scope ends the run with exit code 3 and the line `setup failure: no-program`. The TypeScript analyzer also leaves out JavaScript files that git ignores, that are build output or that are minified by their line lengths, so where a tree's only JavaScript files are such files it finds no program. The run then leaves TypeScript out, shows the analyzer's `no-program` line under its name and a line naming the language left out, and reports the other languages. A consumer that `--scope` declares and that holds only languages the run leaves out, or no language still in scope, is left out too, and the same line names it. A module is then named as it is in a tree with no JavaScript: an analyzer that was handed a module under another name, such as `.` for a target two languages load, runs again, and its files are under `rerun/` in the run directory. Only that second run's output is shown and merged: what the analyzer printed on its first run is not shown, and its first report stays in the run directory beside `rerun/`. A failure of the second run ends the run with that analyzer's own failure line, as on a first run. Where another analyzer in the provider list claims the same language and reports, the language stays in scope, and the line says the run goes on without the analyzer that found no program. Where no analyzer finds a program, the run leaves every language out and names each. With nothing left to analyze, it writes a report that names no language and no finding, with every rendering `--formats` asks for, and exits with 0, so a tree that holds only a `tsconfig.json` with no sources passes. Where Protocol Buffers is then the only language left in scope, deadset reads the schemas alone, as it does in a tree holding only schema files. A language `analysis.languages` names is always analyzed, so there its analyzer's `no-program` ends the run.

## The language census

`deadset print-languages` prints the census of the run `deadset analyze` would make with the same `--target`, `--scope`, `--central` and `--languages`. It plans that run, then stops before any analyzer runs, so it needs no analyzer installed. It does not read the ignore file, the baseline or the edges document, so `analyze` over the same target can still end at the check of those documents described above. A CI job can call it first to learn which analyzers to install:

```json
{
  "languages": [
    {
      "language": "go",
      "provenance": "detected"
    }
  ],
  "analyzers": [
    {
      "name": "deadset-go",
      "languages": [
        "go"
      ]
    }
  ],
  "left_out": []
}
```

- `languages` lists every language in scope. Its `provenance` is `detected` when the tree put the language in scope. When `analysis.languages` named it, the provenance is the source that named it, as `print-config` spells it, such as `flag: --languages`.
- `analyzers` lists every provider entry the run invokes, in the provider list's order, with the languages in scope it is handed. Protocol Buffers, which deadset reads itself, is in no entry.
- `left_out` is always empty: deadset reads every language it detects, and leaves out only a language whose analyzer then finds no program, which `print-languages` cannot know.

The census follows the contract's [census schema](https://github.com/cplieger/deadset-spec/blob/v8.0.0/contract/census.schema.json). Where the plan ends the run, `print-languages` ends the same way and prints no census. A tree with no language in scope exits with 3 and `setup failure: no-program`, and a language no provider entry claims exits with 2. A detected language whose analyzer then finds no program is still left out by `analyze`, as the section above describes, since only that analyzer can tell.

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

Standard output carries the `text` and `github` formats, in the order `--formats` lists them. The `text` format ends with a line that counts the findings `--min-confidence` left out, when it left any out. Next come one line per analyzer that ran, with its version and the sha256 of its binary, and the summary line. Last come the path of each format written as a file, and, when a finding fails the run, one remediation line for each kind of thing the failing findings are about, counting those `reporters.max_findings` leaves out of the list: a declaration, a part of one, an import, a file, a dependency or a configuration record. Each line names the change that removes such a finding and the suppressions that can name it. A file or a configuration record has none, so its line offers no inline directive or `deadset-ignore.json` entry.

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
| `rerun/` | The `scope.json`, `config.<analyzer>.json`, `scope.<analyzer>.json` and `report.<analyzer>.json` of each analyzer run again after a language was left out |
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
