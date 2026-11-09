package schema

import (
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/cplieger/deadset-spec/v8/report"
	"github.com/cplieger/deadset-spec/v8/vocab"
	"github.com/cplieger/deadset/v2/internal/config"
	"github.com/cplieger/deadset/v2/internal/schema/proto"
)

// upstream is schemas.7's list of upstream schemas: an import path pattern
// and the package a copy of it declares, or a package below it where below.
var upstream = []struct {
	path, pkg string
	below     bool
}{
	{"google/protobuf/*.proto", "google.protobuf", false},
	{"google/protobuf/*.proto", "pb", true},
	{"google/protobuf/compiler/plugin.proto", "google.protobuf.compiler", false},
	{"google/api/*.proto", "google.api", false},
	{"google/rpc/*.proto", "google.rpc", false},
	{"google/type/*.proto", "google.type", false},
	{"buf/validate/validate.proto", "buf.validate", false},
	{"validate/validate.proto", "validate", false},
	{"gogoproto/gogo.proto", "gogoproto", false},
	{"protoc-gen-openapiv2/options/*.proto", "grpc.gateway.protoc_gen_openapiv2.options", false},
}

// exempt holds the roots, the records schemas.6 to schemas.8 keep live, and
// the enum values schemas.19 never reports.
func (r *reading) exempt() {
	s := &r.in.Settings
	r.roots()
	if !s.disabled(vocab.SchemaVendored) {
		for _, f := range r.files {
			if f.unit != nil && r.vendored(f) {
				holdAll(f.decls, string(vocab.SchemaVendored))
			}
		}
	}
	if !s.disabled(vocab.SchemaOption) {
		r.exemptOptions()
	}
	for _, d := range r.decls {
		if d.kind == kindEnum {
			r.exemptValues(d, !s.disabled(vocab.SchemaDefaultValue))
		}
	}
}

// exemptOptions is schemas.6 over every option of the target's files.
func (r *reading) exemptOptions() {
	seen := map[*decl]bool{}
	for _, f := range r.files {
		if f.unit != nil {
			for _, name := range optionExtensions(f.unit.File) {
				r.keepReached(r.byFull[name], seen)
			}
		}
	}
}

func holdAll(decls []*decl, reason string) {
	for _, d := range decls {
		d.hold(reason)
	}
}

// exemptValues holds an open enum's first value, schemas.8, and every value
// sharing its number with another, which schemas.19 never reports.
func (r *reading) exemptValues(d *decl, defaults bool) {
	values := d.enum.Values
	if defaults && d.enum.Open && len(values) > 0 {
		r.byRef[d.ref+"."+values[0].Name].hold(string(vocab.SchemaDefaultValue))
	}
	numbers := map[int32]int{}
	for _, v := range values {
		numbers[v.Number]++
	}
	for _, v := range values {
		if numbers[v.Number] > 1 {
			r.byRef[d.ref+"."+v.Name].hold("shared-number")
		}
	}
}

func (r *reading) vendored(f *schemaFile) bool {
	if _, ok := copyPath(f.importPath, func() string { return f.unit.File.Package }); ok {
		return true
	}
	for _, p := range r.in.Settings.VendoredPaths {
		if globPattern(p).MatchString(f.rel) {
			return true
		}
	}
	return false
}

// copyPath is the import path a file of path p has as a copy of an upstream
// schema: the longest end of p, after any /, that a row's path matches with
// the package pkg returns, which is asked only once a path matches.
func copyPath(p string, pkg func() string) (string, bool) {
	declared, asked := "", false
	for tail, more := p, true; more; _, tail, more = strings.Cut(tail, "/") {
		for _, u := range upstream {
			if matched, _ := path.Match(u.path, tail); !matched {
				continue
			}
			if !asked {
				declared, asked = pkg(), true
			}
			if declared == u.pkg || u.below && strings.HasPrefix(declared, u.pkg+".") {
				return tail, true
			}
		}
	}
	return "", false
}

// roots is schemas.24: every declaration an entry of roots.patterns the
// reader's report reads matches is a root, and an entry that matches none is
// an unmatched root unless a schema file of the target is not read cleanly.
func (r *reading) roots() {
	for _, entry := range r.in.Settings.Roots {
		if !readsRoot(entry) {
			continue
		}
		pattern, matched := globPattern(entry), false
		for _, d := range r.decls {
			if pattern.MatchString(d.ref) {
				d.hold("root")
				matched = true
			}
		}
		if !matched && len(r.unclean) == 0 {
			r.checks = append(r.checks, unmatchedRoot(entry, rootsDocument(r.in.Target, r.in.Settings.RootsSource)))
		}
	}
}

// readsRoot is merge.md's rule for which configured strings a report of the
// proto language reads: one whose text before its first :// is proto or holds
// a wildcard, and one with no such text.
func readsRoot(entry string) bool {
	prefix, _, found := strings.Cut(entry, "://")
	return !found || strings.ContainsAny(prefix, "*?") || prefix == string(vocab.Proto)
}

// rootsDocument is the target-relative path of the document source, a
// provenance value, names, as deadset-go positions DS9004; the repository
// configuration where it names none inside target, since a finding has no
// spelling for a file outside it.
func rootsDocument(target, source string) string {
	_, label, named := strings.Cut(source, ": ")
	root, rootErr := filepath.Abs(target)
	document, documentErr := filepath.Abs(label)
	if !named || rootErr != nil || documentErr != nil {
		return config.RepositoryFile
	}
	relative, err := filepath.Rel(root, document)
	if err != nil || !filepath.IsLocal(relative) {
		return config.RepositoryFile
	}
	return filepath.ToSlash(relative)
}

// unmatchedRoot is the DS9004 finding of an entry, at the first position of
// document, its component numbered by mint.
func unmatchedRoot(entry, document string) report.Finding {
	kind, _ := vocab.KindOf(vocab.UnmatchedRoot)
	message := "configured root matches no symbol of the inventory"
	if strings.ContainsAny(entry, "*?") {
		message = "configured root pattern matches no symbol of the inventory"
	}
	return report.Finding{
		Code: vocab.UnmatchedRoot, Kind: kind.Name, Language: vocab.Proto,
		Position:          report.Position{Path: document, Line: 1, Column: 1, EndLine: 1},
		Symbol:            report.Symbol{Ref: entry, Kind: "root", Name: entry, SizeLines: 1},
		ReachabilityClass: kind.MaxClass, Confidence: kind.MaxClass,
		Component:  report.Component{Root: true, SymbolCount: 1},
		RetainedBy: []string{}, Configurations: []string{}, ConsumersLoaded: []string{},
		Fixability: kind.Fixability, Severity: kind.DefaultSeverity, Message: message,
	}
}

// globPattern is a pattern of roots.patterns' syntax: * any run of characters,
// ? one code point, every other character itself.
func globPattern(p string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString("^")
	for _, c := range p {
		switch c {
		case '*':
			b.WriteString("(?s:.*)")
		case '?':
			b.WriteString("(?s:.)")
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString("$")
	return regexp.MustCompile(b.String())
}

// keepReached is schemas.6: an extension an option names is live with every
// declaration its type reaches through field types.
func (r *reading) keepReached(d *decl, seen map[*decl]bool) {
	if d == nil || seen[d] {
		return
	}
	seen[d] = true
	d.hold(string(vocab.SchemaOption))
	for _, ref := range d.uses {
		if t := r.byRef[ref]; t != nil {
			r.keepReached(t, seen)
		}
	}
	if d.kind == kindMessage || d.kind == kindEnum {
		for _, ref := range d.members {
			if m := r.byRef[ref]; m != nil && (m.kind == kindField || m.kind == kindValue) {
				r.keepReached(m, seen)
			}
		}
	}
}

// optionExtensions is the full name of every extension an interpreted option
// of the file sets, inside an aggregate value included.
func optionExtensions(f *proto.File) []string {
	var names []string
	for _, o := range fileOptions(f) {
		if o != nil && o.Interpreted != nil {
			names = extensionsOf(o.Interpreted, names)
		}
	}
	return names
}

func extensionsOf(m *proto.MessageValue, names []string) []string {
	for _, fv := range m.Fields {
		if fv.Field.Extendee != "" {
			names = append(names, fv.Field.FullName)
		}
		for _, v := range fv.Values {
			if v.Message != nil {
				names = extensionsOf(v.Message, names)
			}
		}
	}
	return names
}

// fileOptions is the options of every element of a file.
func fileOptions(f *proto.File) []*proto.Options {
	out := []*proto.Options{f.Options}
	for _, m := range f.Messages {
		out = messageOptions(m, out)
	}
	for _, e := range f.Enums {
		out = enumOptions(e, out)
	}
	for _, s := range f.Services {
		out = append(out, s.Options)
		for _, m := range s.Methods {
			out = append(out, m.Options)
		}
	}
	for _, x := range f.Extensions {
		out = append(out, x.Options)
	}
	return out
}

func messageOptions(m *proto.Message, out []*proto.Options) []*proto.Options {
	out = append(out, m.Options)
	for _, fl := range slices.Concat(m.Fields, m.Extensions) {
		out = append(out, fl.Options)
	}
	for _, o := range m.Oneofs {
		out = append(out, o.Options)
	}
	for _, x := range m.ExtRanges {
		out = append(out, x.Options)
	}
	for _, e := range m.Enums {
		out = enumOptions(e, out)
	}
	for _, n := range m.Nested {
		out = messageOptions(n, out)
	}
	return out
}

func enumOptions(e *proto.Enum, out []*proto.Options) []*proto.Options {
	out = append(out, e.Options)
	for _, v := range e.Values {
		out = append(out, v.Options)
	}
	return out
}
