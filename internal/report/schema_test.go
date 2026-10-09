package report

import (
	"encoding/json"
	"maps"
	"reflect"
	"regexp"
	"regexp/syntax"
	"slices"
	"strconv"
	"strings"
	"testing"

	spec "github.com/cplieger/deadset-spec/v7"
)

// The two schemas the types of this package mirror.
const (
	reportSchema  = "contract/report.schema.json"
	findingSchema = "contract/finding.schema.json"
)

// TestTheTypesDeclareEveryObjectTheSchemasDeclare pins every closed object the
// two schemas declare to the Go type that holds it: the same members in the
// same order, the same required members, and no other member admitted. The two
// shapes of a build configuration are pinned by the decode tests instead,
// because one type reads both.
func TestTheTypesDeclareEveryObjectTheSchemasDeclare(t *testing.T) {
	t.Parallel()

	types := map[string]reflect.Type{
		reportSchema + "#":                                                         reflect.TypeFor[Report](),
		reportSchema + "#/properties/analyzer":                                     reflect.TypeFor[Analyzer](),
		reportSchema + "#/properties/analyzer/properties/conformance":              reflect.TypeFor[Conformance](),
		reportSchema + "#/properties/merged_from/items":                            reflect.TypeFor[InputReport](),
		reportSchema + "#/properties/target":                                       reflect.TypeFor[Target](),
		reportSchema + "#/properties/consumers":                                    reflect.TypeFor[Consumers](),
		reportSchema + "#/properties/consumers/properties/loaded/items":            reflect.TypeFor[LoadedConsumer](),
		reportSchema + "#/properties/consumers/properties/unavailable/items":       reflect.TypeFor[UnavailableConsumer](),
		reportSchema + "#/properties/edge_evaluations/items":                       reflect.TypeFor[EdgeEvaluation](),
		reportSchema + "#/properties/stale_suppressions/items":                     reflect.TypeFor[StaleSuppression](),
		reportSchema + "#/properties/stale_suppressions/items/properties/entry":    reflect.TypeFor[SuppressionEntry](),
		reportSchema + "#/properties/stale_suppressions/items/properties/position": reflect.TypeFor[SuppressionPosition](),
		reportSchema + "#/properties/declared_gaps/items":                          reflect.TypeFor[DeclaredGap](),
		reportSchema + "#/properties/test_file_rules/items":                        reflect.TypeFor[TestFileRule](),
		reportSchema + "#/properties/type_error_skips/items":                       reflect.TypeFor[TypeErrorSkip](),
		reportSchema + "#/properties/notes/items":                                  reflect.TypeFor[Note](),
		reportSchema + "#/properties/unanswered_questions/items":                   reflect.TypeFor[UnansweredQuestion](),
		reportSchema + "#/properties/conventions_applied/items":                    reflect.TypeFor[ConventionApplied](),
		reportSchema + "#/properties/totals":                                       reflect.TypeFor[Totals](),
		reportSchema + "#/properties/totals/properties/by_severity":                reflect.TypeFor[BySeverity](),
		reportSchema + "#/properties/totals/properties/withheld":                   reflect.TypeFor[Withheld](),
		findingSchema + "#":                                           reflect.TypeFor[Finding](),
		findingSchema + "#/$defs/position":                            reflect.TypeFor[Position](),
		findingSchema + "#/$defs/positioned_symbol":                   reflect.TypeFor[Positioned](),
		findingSchema + "#/properties/symbol":                         reflect.TypeFor[Symbol](),
		findingSchema + "#/properties/component":                      reflect.TypeFor[Component](),
		findingSchema + "#/properties/details":                        reflect.TypeFor[Details](),
		findingSchema + "#/properties/details/properties/entry":       reflect.TypeFor[Entry](),
		findingSchema + "#/properties/details/properties/sides/items": reflect.TypeFor[EdgeSide](),
	}
	configurationArms := regexp.MustCompile(`#/properties/configurations(_not_built)?/items/oneOf/[01]$`)

	objects := map[string]json.RawMessage{}
	for _, file := range []string{reportSchema, findingSchema} {
		walk(t, schemaOf(t, file), file+"#", func(location string, node json.RawMessage) {
			var held struct {
				AdditionalProperties json.RawMessage `json:"additionalProperties"`
			}
			decodeSchema(t, node, &held)
			if held.AdditionalProperties != nil && !configurationArms.MatchString(location) {
				objects[location] = node
			}
		})
	}
	if got, want := slices.Sorted(maps.Keys(objects)), slices.Sorted(maps.Keys(types)); !slices.Equal(got, want) {
		t.Fatalf("the schemas declare the closed objects\n%q\nwant one type for each, which the table holds for\n%q", got, want)
	}

	for location, node := range objects {
		var declared struct {
			AdditionalProperties *bool       `json:"additionalProperties"`
			Required             []string    `json:"required"`
			Properties           orderedKeys `json:"properties"`
		}
		decodeSchema(t, node, &declared)
		held := types[location]
		var names, requiredNames []string
		for _, field := range objectOf(reflect.New(held).Elem()).fields {
			names = append(names, field.name)
			if !field.optional {
				requiredNames = append(requiredNames, field.name)
			}
		}
		if declared.AdditionalProperties == nil || *declared.AdditionalProperties {
			t.Errorf("%s admits members it does not declare, which no type of this package reads", location)
		}
		if !slices.Equal(names, declared.Properties) {
			t.Errorf("%s declares the members %q in that order, want %s's %q", location, declared.Properties, held, names)
		}
		if got, want := slices.Sorted(slices.Values(requiredNames)), slices.Sorted(slices.Values(declared.Required)); !slices.Equal(got, want) {
			t.Errorf("%s requires %q, want %s's required members %q", location, want, held, got)
		}
	}
}

// TestThePatternsAreTheSchemas pins every pattern the two schemas declare
// outside a conditional to the regular expression the decode applies there,
// compared as the expressions the two texts parse to.
func TestThePatternsAreTheSchemas(t *testing.T) {
	t.Parallel()

	want := map[string]*regexp.Regexp{
		reportSchema + "#/properties/schema_version":                                               semverPattern,
		reportSchema + "#/properties/contract_version":                                             semverPattern,
		reportSchema + "#/properties/analyzer/properties/name":                                     tokenPattern,
		reportSchema + "#/properties/analyzer/properties/version":                                  versionPattern,
		reportSchema + "#/properties/analyzer/properties/schema_versions_accepted/items":           semverPattern,
		reportSchema + "#/properties/analyzer/properties/conformance/properties/corpus_version":    semverPattern,
		reportSchema + "#/properties/analyzer/properties/conformance/properties/digest":            digestPattern,
		reportSchema + "#/properties/merged_from/items/properties/name":                            tokenPattern,
		reportSchema + "#/properties/merged_from/items/properties/version":                         versionPattern,
		reportSchema + "#/properties/merged_from/items/properties/digest":                          digestPattern,
		reportSchema + "#/properties/target/properties/root":                                       rootPattern,
		reportSchema + "#/properties/configurations/items/oneOf/1/properties/project":              relativePattern,
		reportSchema + "#/properties/configurations_not_built/items/oneOf/0/properties/error":      oneLinePattern,
		reportSchema + "#/properties/configurations_not_built/items/oneOf/1/properties/project":    relativePattern,
		reportSchema + "#/properties/configurations_not_built/items/oneOf/1/properties/error":      oneLinePattern,
		reportSchema + "#/properties/consumers/properties/unavailable/items/properties/reason":     nonBlankPattern,
		reportSchema + "#/properties/edge_evaluations/items/properties/edge":                       edgePattern,
		reportSchema + "#/properties/edge_evaluations/items/properties/analyzer":                   tokenPattern,
		reportSchema + "#/properties/stale_suppressions/items/properties/entry/properties/code":    codePattern,
		reportSchema + "#/properties/stale_suppressions/items/properties/entry/properties/path":    artifactPattern,
		reportSchema + "#/properties/stale_suppressions/items/properties/entry/properties/reason":  nonBlankPattern,
		reportSchema + "#/properties/stale_suppressions/items/properties/position/properties/path": artifactPattern,
		reportSchema + "#/properties/stale_suppressions/items/properties/message":                  nonBlankPattern,
		reportSchema + "#/properties/stale_suppressions/items/properties/analyzer":                 tokenPattern,
		reportSchema + "#/properties/declared_gaps/items/properties/fixture":                       tokenPattern,
		reportSchema + "#/properties/declared_gaps/items/properties/symbol":                        gapSymbolPattern,
		reportSchema + "#/properties/declared_gaps/items/properties/capability":                    capabilityPattern,
		reportSchema + "#/properties/declared_gaps/items/properties/reason":                        nonBlankPattern,
		reportSchema + "#/properties/declared_gaps/items/properties/analyzer":                      tokenPattern,
		reportSchema + "#/properties/excluded_by_cgo/items":                                        artifactPattern,
		reportSchema + "#/properties/test_file_rules/items/properties/rule":                        tokenPattern,
		reportSchema + "#/properties/type_error_skips/items/properties/path":                       artifactPattern,
		reportSchema + "#/properties/type_error_skips/items/properties/message":                    oneLinePattern,
		reportSchema + "#/properties/notes/items/properties/path":                                  artifactPattern,
		reportSchema + "#/properties/notes/items/properties/message":                               oneLinePattern,
		reportSchema + "#/properties/conventions_applied/items/properties/name":                    tokenPattern,
		reportSchema + "#/properties/conventions_applied/items/properties/manifest":                artifactPattern,
		findingSchema + "#/properties/code":                                                        codePattern,
		findingSchema + "#/properties/kind":                                                        tokenPattern,
		findingSchema + "#/properties/symbol/properties/name":                                      oneLinePattern,
		findingSchema + "#/properties/component/properties/id":                                     componentPattern,
		findingSchema + "#/properties/retained_by/items":                                           tokenPattern,
		findingSchema + "#/properties/message":                                                     messagePattern,
		findingSchema + "#/properties/analyzer":                                                    tokenPattern,
		findingSchema + "#/properties/details/properties/entry/properties/code":                    codePattern,
		findingSchema + "#/properties/details/properties/entry/properties/reason":                  nonBlankPattern,
		findingSchema + "#/properties/details/properties/edge":                                     edgePattern,
		findingSchema + "#/$defs/relative_path":                                                    relativePattern,
		findingSchema + "#/$defs/positioned_symbol/properties/name":                                oneLinePattern,
	}

	got := map[string]string{}
	for _, file := range []string{reportSchema, findingSchema} {
		walk(t, schemaOf(t, file), file+"#", func(location string, node json.RawMessage) {
			var held struct {
				Pattern string `json:"pattern"`
			}
			decodeSchema(t, node, &held)
			if held.Pattern != "" {
				got[location] = held.Pattern
			}
		})
	}
	if locations, pinned := slices.Sorted(maps.Keys(got)), slices.Sorted(maps.Keys(want)); !slices.Equal(locations, pinned) {
		t.Fatalf("the schemas declare a pattern at\n%q\nwant the locations the decode applies one at\n%q", locations, pinned)
	}
	for location, pattern := range got {
		if parsed(t, pattern) != parsed(t, want[location].String()) {
			t.Errorf("%s declares %s, want an expression the decode's %s parses to the same", location, pattern, want[location])
		}
	}
}

// parsed is the simplified expression pattern parses to under the syntax
// regexp.Compile reads, so two spellings of one expression compare equal.
func parsed(t *testing.T, pattern string) string {
	t.Helper()

	expression, err := syntax.Parse(pattern, syntax.Perl)
	if err != nil {
		t.Fatalf("Setup: parse the pattern %q: %v", pattern, err)
	}
	return expression.Simplify().String()
}

// TestTheVocabulariesAreTheSchemas pins every enumeration and constant the two
// schemas declare outside a conditional to the vocabulary the decode holds a
// value to there, compared as sets.
func TestTheVocabulariesAreTheSchemas(t *testing.T) {
	t.Parallel()

	want := map[string][]string{
		reportSchema + "#/properties/analyzer/properties/languages/items":                    languages,
		reportSchema + "#/properties/analyzer/properties/conformance/properties/result":      names(results),
		reportSchema + "#/properties/target/properties/kind":                                 names(targetKinds),
		reportSchema + "#/properties/consumers/properties/loaded/items/properties/role":      consumerRoles,
		reportSchema + "#/properties/consumers/properties/unavailable/items/properties/role": consumerRoles,
		reportSchema + "#/properties/edge_evaluations/items/properties/side":                 names(sides),
		reportSchema + "#/properties/edge_evaluations/items/properties/state":                names(states),
		reportSchema + "#/properties/stale_suppressions/items/properties/code":               staleCodes,
		reportSchema + "#/properties/stale_suppressions/items/properties/mechanism":          mechanisms,
		reportSchema + "#/properties/notes/items/properties/kind":                            names(noteKinds),
		reportSchema + "#/properties/notes/items/properties/key":                             noteKeys,
		findingSchema + "#/properties/language":                                              languages,
		findingSchema + "#/properties/symbol/properties/kind":                                symbolKinds,
		findingSchema + "#/properties/reachability_class":                                    names(classes),
		findingSchema + "#/properties/confidence":                                            names(classes),
		findingSchema + "#/properties/liveness_relation":                                     names(relations),
		findingSchema + "#/properties/fixability":                                            names(fixabilities),
		findingSchema + "#/properties/severity":                                              names(severities),
		findingSchema + "#/properties/details/properties/narrower_visibility":                visibilities,
		findingSchema + "#/properties/details/properties/dependency_class":                   dependencyKinds,
		findingSchema + "#/properties/details/properties/mechanism":                          mechanisms,
		findingSchema + "#/properties/details/properties/sides/items/properties/side":        names(sides),
		findingSchema + "#/properties/details/properties/sides/items/properties/state":       names(states),
	}

	got := map[string][]string{}
	for _, file := range []string{reportSchema, findingSchema} {
		walk(t, schemaOf(t, file), file+"#", func(location string, node json.RawMessage) {
			var held struct {
				Const *string  `json:"const"`
				Enum  []string `json:"enum"`
			}
			decodeSchema(t, node, &held)
			switch {
			case held.Const != nil:
				got[location] = []string{*held.Const}
			case held.Enum != nil:
				got[location] = held.Enum
			}
		})
	}
	if locations, pinned := slices.Sorted(maps.Keys(got)), slices.Sorted(maps.Keys(want)); !slices.Equal(locations, pinned) {
		t.Fatalf("the schemas declare a vocabulary at\n%q\nwant the locations the decode holds one at\n%q", locations, pinned)
	}
	for location, vocabulary := range got {
		if !slices.Equal(slices.Sorted(slices.Values(vocabulary)), slices.Sorted(slices.Values(want[location]))) {
			t.Errorf("%s declares %q, want the decode's %q", location, vocabulary, want[location])
		}
	}
}

// TestTheConditionalRulesAreTheSchemas pins the rules the two schemas state
// under if: which code carries which details member, the language the
// excluded_by rule reads, the fixability removes_last_use_of needs, the
// subjects and codes that carry no liveness relation, the state that carries a
// pending finding, and the one rule the report schema adds to a reported
// finding.
func TestTheConditionalRulesAreTheSchemas(t *testing.T) {
	t.Parallel()

	var finding struct {
		AllOf []struct {
			If struct {
				AnyOf []struct {
					Properties struct {
						Symbol struct {
							Properties struct {
								Kind struct {
									Enum []string `json:"enum"`
								} `json:"kind"`
							} `json:"properties"`
						} `json:"symbol"`
						Code struct {
							Enum []string `json:"enum"`
						} `json:"code"`
					} `json:"properties"`
				} `json:"anyOf"`
				Properties struct {
					Code struct {
						Const string   `json:"const"`
						Enum  []string `json:"enum"`
					} `json:"code"`
					Fixability struct {
						Const string `json:"const"`
					} `json:"fixability"`
				} `json:"properties"`
			} `json:"if"`
			Then struct {
				AllOf []struct {
					If struct {
						Properties struct {
							Language struct {
								Const string `json:"const"`
							} `json:"language"`
						} `json:"properties"`
					} `json:"if"`
					Then detailsRule `json:"then"`
				} `json:"allOf"`
				detailsRule
			} `json:"then"`
			Else struct {
				Properties struct {
					Details struct {
						Not struct {
							Required []string `json:"required"`
						} `json:"not"`
					} `json:"details"`
				} `json:"properties"`
			} `json:"else"`
		} `json:"allOf"`
	}
	decodeSchema(t, schemaOf(t, findingSchema), &finding)

	branches := map[string][]string{}
	var excludedBy, removesLastUse, relationFree int
	for _, rule := range finding.AllOf {
		codes := rule.If.Properties.Code.Enum
		if rule.If.Properties.Code.Const != "" {
			codes = []string{rule.If.Properties.Code.Const}
		}
		switch {
		case len(rule.Then.AllOf) == 1:
			excludedBy++
			nested := rule.Then.AllOf[0]
			if !slices.Equal(codes, []string{excludedByCode}) || nested.If.Properties.Language.Const != "go" ||
				!slices.Equal(nested.Then.Properties.Details.Required, []string{"excluded_by"}) {
				t.Errorf("the excluded_by rule reads %q under the language %q requiring %q, want %q under go requiring excluded_by",
					codes, nested.If.Properties.Language.Const, nested.Then.Properties.Details.Required, excludedByCode)
			}
		case rule.If.Properties.Fixability.Const != "":
			removesLastUse++
			if rule.If.Properties.Fixability.Const != string(FixabilityDeletable) ||
				!slices.Equal(rule.Else.Properties.Details.Not.Required, []string{"removes_last_use_of"}) {
				t.Errorf("the fixability rule admits %q only under %q, want removes_last_use_of only under deletable",
					rule.Else.Properties.Details.Not.Required, rule.If.Properties.Fixability.Const)
			}
		case len(rule.If.AnyOf) == 2:
			relationFree++
			kinds := rule.If.AnyOf[0].Properties.Symbol.Properties.Kind.Enum
			relationCodes := rule.If.AnyOf[1].Properties.Code.Enum
			if !slices.Equal(slices.Sorted(slices.Values(kinds)), slices.Sorted(slices.Values(relationFreeKinds))) ||
				!slices.Equal(slices.Sorted(slices.Values(relationCodes)), slices.Sorted(slices.Values(relationFreeCodes))) {
				t.Errorf("the liveness rule frees the kinds %q and the codes %q, want %q and %q",
					kinds, relationCodes, relationFreeKinds, relationFreeCodes)
			}
		default:
			for _, member := range rule.Then.Properties.Details.Required {
				branches[member] = codes
			}
		}
	}
	if excludedBy != 1 || removesLastUse != 1 || relationFree != 1 {
		t.Errorf("the finding schema states %d excluded_by, %d removes_last_use_of and %d liveness rules, want one of each",
			excludedBy, removesLastUse, relationFree)
	}
	pinned := map[string][]string{}
	for _, branch := range detailBranches {
		pinned[branch.member] = branch.codes
	}
	if !maps.EqualFunc(branches, pinned, slices.Equal) {
		t.Errorf("the finding schema ties the details members to the codes %v, want the decode's %v", branches, pinned)
	}

	var report struct {
		Properties struct {
			Findings struct {
				Items struct {
					Properties struct {
						RetainedBy struct {
							MaxItems *int `json:"maxItems"`
						} `json:"retained_by"`
					} `json:"properties"`
				} `json:"items"`
			} `json:"findings"`
			EdgeEvaluations struct {
				Items struct {
					If struct {
						Properties struct {
							State struct {
								Const string `json:"const"`
							} `json:"state"`
						} `json:"properties"`
					} `json:"if"`
					Then struct {
						Required []string `json:"required"`
					} `json:"then"`
				} `json:"items"`
			} `json:"edge_evaluations"`
		} `json:"properties"`
	}
	decodeSchema(t, schemaOf(t, reportSchema), &report)
	if most := report.Properties.Findings.Items.Properties.RetainedBy.MaxItems; most == nil || *most != 0 {
		t.Errorf("the report schema bounds a reported finding's retained_by at %v, want 0", most)
	}
	if evaluation := report.Properties.EdgeEvaluations.Items; evaluation.If.Properties.State.Const != string(StateDead) ||
		!slices.Equal(evaluation.Then.Required, []string{"finding"}) {
		t.Errorf("the report schema requires %q of an evaluation in the state %q, want a finding of a dead one",
			evaluation.Then.Required, evaluation.If.Properties.State.Const)
	}
}

// detailsRule is the then of a rule that requires details members.
type detailsRule struct {
	Properties struct {
		Details struct {
			Required []string `json:"required"`
		} `json:"details"`
	} `json:"properties"`
}

// orderedKeys is the member names of a JSON object in the order the document
// writes them.
type orderedKeys []string

func (k *orderedKeys) UnmarshalJSON(data []byte) error {
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	if _, err := decoder.Token(); err != nil {
		return err
	}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return err
		}
		*k = append(*k, key.(string))
		var skipped json.RawMessage
		if err := decoder.Decode(&skipped); err != nil {
			return err
		}
	}
	return nil
}

// walk visits node and every schema below it that describes a value a decode
// reads, giving each its location as a JSON Pointer fragment. It does not
// descend into a conditional (allOf, if, then, else), whose rules the
// conditional test pins.
func walk(t *testing.T, node json.RawMessage, location string, visit func(string, json.RawMessage)) {
	t.Helper()

	visit(location, node)
	var keywords struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Defs       map[string]json.RawMessage `json:"$defs"`
		Items      json.RawMessage            `json:"items"`
		OneOf      []json.RawMessage          `json:"oneOf"`
	}
	decodeSchema(t, node, &keywords)
	for name, child := range keywords.Properties {
		walk(t, child, location+"/properties/"+name, visit)
	}
	for name, child := range keywords.Defs {
		walk(t, child, location+"/$defs/"+name, visit)
	}
	if keywords.Items != nil {
		walk(t, keywords.Items, location+"/items", visit)
	}
	for i, arm := range keywords.OneOf {
		walk(t, arm, location+"/oneOf/"+strconv.Itoa(i), visit)
	}
}

// schemaOf is one schema of the pinned Contract.
func schemaOf(t *testing.T, file string) json.RawMessage {
	t.Helper()

	return read(t, spec.Contract, file)
}

// decodeSchema reads one schema node into the typed into.
func decodeSchema(t *testing.T, node json.RawMessage, into any) {
	t.Helper()

	if err := json.Unmarshal(node, into); err != nil {
		t.Fatalf("Setup: decode the schema node %s: %v", node, err)
	}
}

// names is a typed vocabulary spelled as strings.
func names[T ~string](vocabulary []T) []string {
	spelled := make([]string, 0, len(vocabulary))
	for _, word := range vocabulary {
		spelled = append(spelled, string(word))
	}
	return spelled
}
