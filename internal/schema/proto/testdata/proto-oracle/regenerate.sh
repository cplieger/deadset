#!/bin/sh
# Rewrites descriptors/ and verdicts.json from protoc v36.2, the version the
# front end follows. protobuf/ holds protobuf v36.2's own files verbatim
# (protobuf/LICENSE); corpus.json lists the ones compared and their roots.
# Usage: regenerate.sh /path/to/protoc-36.2
set -eu
protoc=${1:?usage: regenerate.sh /path/to/protoc}
cd "$(dirname "$0")/../.."
DEADSET_PROTOC=$protoc go test -count=1 -run '^TestRegenerateProtocOracle$' .
