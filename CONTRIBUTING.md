# Contributing to deadset

The [shared rules](https://github.com/cplieger/.github/blob/main/CONTRIBUTING.md) for commits, releases, synced files and checks apply here.

## Checks

CI installs neither analyzer, so every test that runs a real one skips there. A change that breaks a real run can still pass CI.

Run those tests before you change how deadset starts an analyzer or reads its report, or change an archive under `testdata/harness/`.

Install the `deadset-go` and `deadset-ts` versions that `testdata/harness/result.json` records, with `go install github.com/cplieger/deadset-go/cmd/deadset-go@v<version>` and `npm install --global @cplieger/deadset-ts@<version>`. Then run `go test ./...`. A newer analyzer can implement a report schema this module does not read yet, and the tests then fail.
