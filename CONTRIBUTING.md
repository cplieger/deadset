# Contributing to deadset

The [shared rules](https://github.com/cplieger/.github/blob/main/CONTRIBUTING.md) for commits, releases, synced files and checks apply here.

## Checks

CI installs neither analyzer, so every test that runs a real one skips there. A change that breaks a real run can still pass CI.

Run those tests before you change how deadset starts an analyzer or reads its report, or change an archive under `testdata/harness/`.

Install the analyzer versions that cplieger/ci pins in [`deadset-ci.yaml`](https://github.com/cplieger/ci/blob/main/.github/workflows/deadset-ci.yaml): `go install github.com/cplieger/deadset-go/v2/cmd/deadset-go@<DEADSET_GO_VERSION>` and `npm install --global @cplieger/deadset-ts@<DEADSET_TS_VERSION>`. Then run `go test ./...`. The tests expect what those versions write. An analyzer on a newer report schema than this module reads fails them.

The harness tests compare report bytes only for the analyzer builds that `testdata/harness/result.json` records. For any other build, they compare the run's outcome.
