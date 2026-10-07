package render

import "github.com/cplieger/deadset/internal/report"

// The SARIF document, one field per property the Contract's mapping emits, so
// two renderings of one report are one byte sequence. A property the mapping
// never emits has no field.

//nolint:govet // fieldalignment: the field order is the mapping's property order, which the document writes
type sarifLog struct {
	Schema     string              `json:"$schema"`
	Version    string              `json:"version"`
	Runs       []sarifRun          `json:"runs"`
	Properties *sarifLogProperties `json:"properties,omitempty"`
}

//nolint:govet // fieldalignment: the field order is the mapping's property order, which the document writes
type sarifLogProperties struct {
	Totals   report.Totals `json:"totals"`
	Withheld string        `json:"withheld,omitzero"`
}

//nolint:govet // fieldalignment: the field order is the mapping's property order, which the document writes
type sarifRun struct {
	Tool               sarifTool               `json:"tool"`
	AutomationDetails  sarifAutomationDetails  `json:"automationDetails"`
	ColumnKind         string                  `json:"columnKind"`
	OriginalURIBaseIDs map[string]sarifURIBase `json:"originalUriBaseIds"`
	Results            []sarifResult           `json:"results"`
	Properties         sarifRunProperties      `json:"properties"`
}

type sarifTool struct {
	Driver sarifDriver `json:"driver"`
}

type sarifDriver struct {
	Name            string      `json:"name"`
	Version         string      `json:"version"`
	SemanticVersion string      `json:"semanticVersion"`
	Rules           []sarifRule `json:"rules"`
}

type sarifRule struct {
	ID                   string              `json:"id"`
	Name                 string              `json:"name"`
	ShortDescription     sarifMessage        `json:"shortDescription"`
	FullDescription      sarifMessage        `json:"fullDescription"`
	Help                 sarifMessage        `json:"help"`
	DefaultConfiguration sarifConfiguration  `json:"defaultConfiguration"`
	Properties           sarifRuleProperties `json:"properties"`
}

type sarifConfiguration struct {
	Level string `json:"level"`
}

type sarifRuleProperties struct {
	Precision string       `json:"precision"`
	Problem   sarifProblem `json:"problem"`
}

type sarifProblem struct {
	Severity string `json:"severity"`
}

type sarifAutomationDetails struct {
	ID string `json:"id"`
}

type sarifURIBase struct {
	Description sarifMessage `json:"description"`
}

//nolint:govet // fieldalignment: the field order is the mapping's property order, which the document writes
type sarifRunProperties struct {
	Totals   report.Totals `json:"totals"`
	Withheld string        `json:"withheld,omitzero"`
}

//nolint:govet // fieldalignment: the field order is the mapping's property order, which the document writes
type sarifResult struct {
	RuleID              string                 `json:"ruleId"`
	RuleIndex           int                    `json:"ruleIndex"`
	Level               string                 `json:"level"`
	Message             sarifMessage           `json:"message"`
	Locations           []sarifLocation        `json:"locations"`
	RelatedLocations    []sarifLocation        `json:"relatedLocations,omitempty"`
	PartialFingerprints sarifFingerprints      `json:"partialFingerprints"`
	Properties          *sarifResultProperties `json:"properties,omitempty"`
}

type sarifMessage struct {
	Text string `json:"text"`
}

//nolint:govet // fieldalignment: the field order is the mapping's property order, which the document writes
type sarifLocation struct {
	ID               int           `json:"id,omitempty"`
	PhysicalLocation sarifPhysical `json:"physicalLocation"`
	Message          *sarifMessage `json:"message,omitempty"`

	// path is the position's path unencoded, which a message link names.
	path string
}

type sarifPhysical struct {
	ArtifactLocation sarifArtifactLocation `json:"artifactLocation"`
	Region           sarifRegion           `json:"region"`
}

type sarifArtifactLocation struct {
	URI       string `json:"uri"`
	URIBaseID string `json:"uriBaseId"`
}

type sarifRegion struct {
	StartLine   int `json:"startLine"`
	StartColumn int `json:"startColumn"`
	EndLine     int `json:"endLine"`
}

type sarifFingerprints struct {
	PrimaryLocationLineHash string `json:"primaryLocationLineHash"`
	DeadsetSymbolRef        string `json:"deadsetSymbolRef/v1"`
}

// sarifResultProperties is every member of a finding the mapping places
// nowhere else, under its schema name; a member the finding omits is absent.
//
//nolint:govet // fieldalignment: the field order is the finding schema's member order
type sarifResultProperties struct {
	Language          string            `json:"language"`
	Symbol            report.Symbol     `json:"symbol"`
	ReachabilityClass report.Class      `json:"reachability_class"`
	Confidence        report.Class      `json:"confidence"`
	LivenessRelation  report.Relation   `json:"liveness_relation,omitzero"`
	TestOnly          bool              `json:"test_only"`
	Generated         bool              `json:"generated"`
	Component         report.Component  `json:"component"`
	RetainedBy        []string          `json:"retained_by"`
	Configurations    []string          `json:"configurations"`
	ConsumersLoaded   []string          `json:"consumers_loaded"`
	Fixability        report.Fixability `json:"fixability"`
	Details           report.Details    `json:"details"`
}
