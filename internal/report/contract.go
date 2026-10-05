// Package report is the deadset report document as Go values: the envelope one
// analyzer writes over one analysis, the envelope a merge writes over several,
// and the finding both carry. The types mirror contract/report.schema.json and
// contract/finding.schema.json member for member, in the schemas' order.
//
// [Decode] is the one way a document becomes a [Report]: it refuses a member
// the schemas do not declare, a required member that is absent or null, a
// member named twice, and every value the schemas refuse, naming the value at
// fault by its JSON Pointer in an [*Error]. [Encode] writes a report in the
// encoding a merged report is compared in, and refuses a report Decode would
// refuse, so a document this package writes is one it reads back.
//
// Decode does not judge the version a document names: admission against an
// accepted schema range, and the conformance result a merge requires, are the
// reader's decisions over the decoded values.
package report

// ContractVersion is the version of the deadset Contract this module
// implements. A report this module writes names it in contract_version, and the
// version verb prints it.
const ContractVersion = "5.3.0"

// SchemaVersions is every report schema version the Contract admits, in
// ascending order: the versions a merge accepts and a handshake admits an
// analyzer under. A merge admits a report by comparing [Report.SchemaVersion]
// with the versions it accepts rather than by a decode failing.
var SchemaVersions = []string{SchemaVersion}

// SchemaVersion is the report schema version the types of this package model,
// the latest of [SchemaVersions], and the version a report this module writes
// names.
const SchemaVersion = "7.0.0"
