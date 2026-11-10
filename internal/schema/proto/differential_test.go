package proto

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
)

// The differential compares Link with protoc v36.2 over protobuf v36.2's own
// src, editions, conformance and upb files: every file protoc accepts must
// read to the descriptor protoc writes with --retain_options and
// --include_source_info, and every mutant the operators below make must get
// protoc's verdict and, for an error in the file itself, protoc's first
// message. It measures nothing beyond those operators.
const oracleDir = "testdata/proto-oracle"

type oracleFile struct {
	File  string   `json:"file"`
	Roots []string `json:"roots"`
}

type oracleVerdict struct {
	File  string `json:"file"`
	Op    string `json:"op"`
	Error string `json:"error,omitempty"`
	Roots string `json:"roots"`
	Fails bool   `json:"fails"`
}

func loadJSON[T any](t *testing.T, name string) T {
	t.Helper()
	var v T
	b, err := os.ReadFile(filepath.Join(oracleDir, name))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func (o oracleFile) rootDirs() []string {
	out := make([]string, len(o.Roots))
	for i, r := range o.Roots {
		out[i] = filepath.Join(oracleDir, "protobuf", r)
	}
	return out
}

func (o oracleFile) fsRoots() []fs.FS {
	var out []fs.FS
	for _, d := range o.rootDirs() {
		out = append(out, os.DirFS(d))
	}
	return out
}

func (o oracleFile) key() string { return strings.Join(o.Roots, ",") + ":" + o.File }

func (o oracleFile) descriptorPath() string {
	name := strings.NewReplacer("/", "__", ",", "+", ":", "--").Replace(o.key())
	return filepath.Join(oracleDir, "descriptors", name+".pb")
}

func (o oracleFile) source(t *testing.T) []byte {
	t.Helper()
	for _, d := range o.rootDirs() {
		if b, err := os.ReadFile(filepath.Join(d, o.File)); err == nil {
			return b
		}
	}
	t.Fatalf("%s: not found under its roots", o.key())
	return nil
}

func TestDifferentialAgainstProtoc(t *testing.T) {
	for _, o := range loadJSON[[]oracleFile](t, "corpus.json") {
		units, _ := Link(o.fsRoots(), Files{Names: []string{o.File}})
		if bad, msg := verdict(units, o.File, map[string]bool{}); bad {
			t.Errorf("%s: refused, protoc accepts it: %s", o.key(), msg)
			continue
		}
		set, err := os.ReadFile(o.descriptorPath())
		if err != nil {
			t.Fatal(err)
		}
		dec := newDescriptorDecoder(units)
		want := dec.file(set)
		if dec.err != nil {
			t.Fatalf("%s: decoding protoc's descriptor: %v", o.key(), dec.err)
		}
		got, exp := factsOf(units[o.File].File), factsOf(want)
		for _, c := range []struct {
			name      string
			got, want []string
			ordered   bool
		}{
			{"header", got.header, exp.header, false},
			{"declarations", got.decls, exp.decls, false},
			{"options", got.options, exp.options, false},
			{"locations", got.locs, exp.locs, true},
		} {
			if d := lineDiff(c.got, c.want, c.ordered); d != "" {
				t.Errorf("%s: %s differ from protoc's:\n%s", o.key(), c.name, d)
			}
		}
	}
}

// verdict reports whether Link refused name or a file it imports, with the
// message protoc would print first for an error in name itself.
func verdict(units map[string]*Unit, name string, seen map[string]bool) (bad bool, msg string) {
	if seen[name] {
		return false, ""
	}
	seen[name] = true
	u := units[name]
	switch {
	case u == nil:
		return true, name + ": File not found."
	case u.Err != nil:
		var es Errors
		if errors.As(u.Err, &es) && len(es) > 0 {
			if es[0].Line == 0 {
				return true, name + ": " + es[0].Message
			}
			return true, fmt.Sprintf("%s:%d:%d: %s", name, es[0].Line, es[0].Column, es[0].Message)
		}
		return true, name + ": " + u.Err.Error()
	case len(u.Missing) > 0:
		return true, ""
	case len(u.Unresolved) > 0:
		return true, ""
	}
	for _, d := range slices.Concat(u.File.Deps, u.File.OptionDeps) {
		if bad, _ := verdict(units, d, seen); bad {
			return true, ""
		}
	}
	return false, ""
}

func TestInvalidInputAgainstProtoc(t *testing.T) {
	want := map[string]oracleVerdict{}
	for _, v := range loadJSON[[]oracleVerdict](t, "verdicts.json") {
		want[v.Roots+":"+v.File+"#"+v.Op] = v
	}
	seen := 0
	for _, o := range loadJSON[[]oracleFile](t, "corpus.json") {
		for _, m := range mutants(o.source(t)) {
			seen++
			key := o.key() + "#" + m.op
			w, ok := want[key]
			if !ok {
				t.Errorf("%s: no protoc verdict; run testdata/proto-oracle/regenerate.sh", key)
				continue
			}
			overlay := fstest.MapFS{o.File: {Data: m.src}}
			units, _ := Link(append([]fs.FS{overlay}, o.fsRoots()...), Files{Names: []string{o.File}})
			bad, msg := verdict(units, o.File, map[string]bool{})
			switch {
			case bad != w.Fails:
				t.Errorf("%s: refused %v, protoc %v (%s)", key, bad, w.Fails, w.Error)
			case msg != "" && msg != w.Error:
				t.Errorf("%s: first error\n  got  %s\n  want %s", key, msg, w.Error)
			}
		}
	}
	if seen != len(want) {
		t.Errorf("%d mutants, %d protoc verdicts; run testdata/proto-oracle/regenerate.sh", seen, len(want))
	}
}

// TestRegenerateProtocOracle writes descriptors/ and verdicts.json with the
// protoc DEADSET_PROTOC names; regenerate.sh runs it.
func TestRegenerateProtocOracle(t *testing.T) {
	protoc := os.Getenv("DEADSET_PROTOC")
	if protoc == "" {
		t.Skip("DEADSET_PROTOC is not set")
	}
	if out, err := exec.Command(protoc, "--version").Output(); err != nil || strings.TrimSpace(string(out)) != "libprotoc 36.2" {
		t.Fatalf("%s --version = %q, %v; want libprotoc 36.2", protoc, out, err)
	}
	include, err := filepath.Abs("include")
	if err != nil {
		t.Fatal(err)
	}
	run := func(roots []string, file, out string) (bool, string) {
		args := []string{"--retain_options", "--include_source_info", "--descriptor_set_out=" + out}
		for _, r := range append(roots, include) {
			args = append(args, "-I"+r)
		}
		var stderr bytes.Buffer
		cmd := exec.Command(protoc, append(args, file)...)
		cmd.Stderr = &stderr
		err := cmd.Run()
		for l := range strings.SplitSeq(stderr.String(), "\n") {
			if l != "" && !strings.Contains(l, ": warning: ") {
				for _, r := range roots {
					l = strings.TrimPrefix(l, r+string(filepath.Separator))
				}
				return err != nil, l
			}
		}
		return err != nil, ""
	}
	if err := os.RemoveAll(filepath.Join(oracleDir, "descriptors")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(oracleDir, "descriptors"), 0o750); err != nil {
		t.Fatal(err)
	}
	verdicts := []oracleVerdict{}
	for _, o := range loadJSON[[]oracleFile](t, "corpus.json") {
		if fails, msg := run(o.rootDirs(), o.File, o.descriptorPath()); fails {
			t.Fatalf("%s: protoc refuses it: %s", o.key(), msg)
		}
		for _, m := range mutants(o.source(t)) {
			dir := t.TempDir()
			if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(o.File)), 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, o.File), m.src, 0o600); err != nil {
				t.Fatal(err)
			}
			fails, msg := run(append([]string{dir}, o.rootDirs()...), o.File, filepath.Join(dir, "out.pb"))
			if !fails {
				msg = ""
			}
			verdicts = append(verdicts, oracleVerdict{Roots: strings.Join(o.Roots, ","), File: o.File, Op: m.op, Fails: fails, Error: msg})
		}
	}
	b, err := json.MarshalIndent(verdicts, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oracleDir, "verdicts.json"), append(b, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

// facts is a File reduced to the lines the differential compares.
type facts struct{ header, decls, options, locs []string }

func factsOf(f *File) facts {
	var r facts
	syntax := f.Syntax
	if syntax == "" {
		syntax = syntaxProto2
	}
	r.header = append(r.header, "package="+f.Package, "syntax="+syntax, "edition="+f.Edition)
	for _, d := range f.Deps {
		r.header = append(r.header, "dep="+d)
	}
	for _, d := range f.OptionDeps {
		r.header = append(r.header, "option-dep="+d)
	}
	r.header = append(r.header, fmt.Sprint("public=", f.Public), fmt.Sprint("weak=", f.Weak))
	r.opts([]int32{fdOptions}, f.Options)
	for i, m := range f.Messages {
		r.message([]int32{fdMessageType, int32(i)}, m)
	}
	for i, e := range f.Enums {
		r.enum([]int32{fdEnumType, int32(i)}, e)
	}
	for i, s := range f.Services {
		p := []int32{fdService, int32(i)}
		r.decl(p, "service", s.Name)
		r.opts(cat(p, sdOptions), s.Options)
		for j, m := range s.Methods {
			mp := cat(p, sdMethod, int32(j))
			r.decl(mp, "method", m.Name, m.Input, m.Output, m.ClientStream, m.ServerStream)
			r.opts(cat(mp, mdOptions), m.Options)
		}
	}
	for i, x := range f.Extensions {
		r.field([]int32{fdExtension, int32(i)}, x)
	}
	for _, l := range f.Locations {
		r.locs = append(r.locs, fmt.Sprintf("%s|%v|L=%q|T=%q|D=%q", pathText(l.Path), l.Span, l.Leading, l.Trailing, l.Detached))
	}
	return r
}

func pathText(p []int32) string {
	parts := make([]string, len(p))
	for i, x := range p {
		parts[i] = strconv.Itoa(int(x))
	}
	return strings.Join(parts, ".")
}

func (r *facts) decl(path []int32, kind string, attrs ...any) {
	r.decls = append(r.decls, pathText(path)+"|"+kind+"|"+fmt.Sprint(attrs...))
}

func (r *facts) message(p []int32, m *Message) {
	r.decl(p, "message", m.Name, " vis=", m.Visibility, " reserved=", m.ReservedRanges, m.ReservedNames)
	r.opts(cat(p, dpOptions), m.Options)
	if m.MapEntry {
		r.options = append(r.options, pathText(cat(p, dpOptions))+"|.7=\"true\"")
	}
	for i, fl := range m.Fields {
		r.field(cat(p, dpField, int32(i)), fl)
	}
	for i, n := range m.Nested {
		r.message(cat(p, dpNested, int32(i)), n)
	}
	for i, e := range m.Enums {
		r.enum(cat(p, dpEnumType, int32(i)), e)
	}
	for i, x := range m.ExtRanges {
		rp := cat(p, dpExtRange, int32(i))
		r.decl(rp, "extension-range", x.Start, " ", x.End)
		r.opts(cat(rp, erOptions), x.Options)
	}
	for i, x := range m.Extensions {
		r.field(cat(p, dpExtension, int32(i)), x)
	}
	for i, o := range m.Oneofs {
		op := cat(p, dpOneof, int32(i))
		r.decl(op, "oneof", o.Name)
		r.opts(cat(op, odOptions), o.Options)
	}
}

func (r *facts) field(p []int32, fl *Field) {
	def, oneof := "-", "-"
	if fl.Default != nil {
		def = strconv.Quote(descriptorDefault(fl))
	}
	if fl.OneofIndex != nil {
		oneof = strconv.Itoa(int(*fl.OneofIndex))
	}
	json := toJSONName(fl.Name)
	if fl.JSONName != nil {
		json = *fl.JSONName
	}
	r.decl(p, "field", fl.Name, " ", fl.Number, " ", fl.Label, " ", fl.Type, " ", fl.TypeName, " ", fl.Extendee,
		" default=", def, " oneof=", oneof, " json=", json, " proto3_optional=", fl.Proto3Optional)
	r.opts(cat(p, fldOptions), fl.Options)
}

// descriptorDefault is a field's default as protoc writes it back from the
// built descriptor: a float's through SimpleFtoa, the rest as parsed.
func descriptorDefault(fl *Field) string {
	v, err := strconv.ParseFloat(*fl.Default, 32)
	if fl.Type != fieldFloat || err != nil || math.IsInf(v, 0) || math.IsNaN(v) {
		return *fl.Default
	}
	s := strconv.FormatFloat(v, 'g', 6, 32)
	if back, _ := strconv.ParseFloat(s, 32); back != v {
		s = strconv.FormatFloat(v, 'g', 9, 32)
	}
	return s
}

func (r *facts) enum(p []int32, e *Enum) {
	r.decl(p, "enum", e.Name, " vis=", e.Visibility, " reserved=", e.ReservedRanges, e.ReservedNames)
	r.opts(cat(p, edOptions), e.Options)
	for i, v := range e.Values {
		vp := cat(p, edValue, int32(i))
		r.decl(vp, "value", v.Name, " ", v.Number)
		r.opts(cat(vp, evOptions), v.Options)
	}
}

// opts lists every option leaf an element sets, as element path, field
// number path and value; an option left uninterpreted is a line of its own.
func (r *facts) opts(p []int32, o *Options) {
	if o == nil {
		return
	}
	for _, u := range o.Uninterpreted {
		r.options = append(r.options, pathText(p)+"|uninterpreted "+fmt.Sprint(u.Name))
	}
	var leaves func(prefix string, m *MessageValue)
	leaves = func(prefix string, m *MessageValue) {
		for n, fv := range m.Fields {
			for _, v := range fv.Values {
				key := prefix + "." + strconv.Itoa(int(n))
				if v.Message != nil {
					leaves(key, v.Message)
					continue
				}
				r.options = append(r.options, pathText(p)+"|"+key+"="+strconv.Quote(v.Scalar))
			}
		}
	}
	if o.Interpreted != nil {
		leaves("", o.Interpreted)
	}
}

// lineDiff lists the first lines got and want do not share, as a multiset or
// in order.
func lineDiff(got, want []string, ordered bool) string {
	var out []string
	if ordered {
		for i := range max(len(got), len(want)) {
			if i >= len(got) || i >= len(want) || got[i] != want[i] {
				out = append(out, fmt.Sprintf("  at %d: got %s\n         want %s", i, at(got, i), at(want, i)))
				break
			}
		}
	} else {
		count := map[string]int{}
		for _, l := range want {
			count[l]++
		}
		for _, l := range got {
			count[l]--
		}
		for _, l := range slices.Sorted(func(yield func(string) bool) {
			for k := range count {
				if !yield(k) {
					return
				}
			}
		}) {
			switch {
			case count[l] > 0:
				out = append(out, "  missing "+l)
			case count[l] < 0:
				out = append(out, "  extra   "+l)
			}
		}
	}
	return strings.Join(out[:min(len(out), 20)], "\n")
}

func at(ls []string, i int) string {
	if i < len(ls) {
		return ls[i]
	}
	return "(none)"
}

type mutant struct {
	op  string
	src []byte
}

var (
	fieldLine    = regexp.MustCompile(`(?m)^([ \t]*)((?:repeated|optional|required)\s+)?([A-Za-z_.][\w.]*)\s+(\w+)\s*=\s*(\d+)\s*([;\[])`)
	syntaxLine   = regexp.MustCompile(`(?m)^\s*(syntax|edition)\s*=\s*"([^"]+)"`)
	messageOpen  = regexp.MustCompile(`\bmessage\s+\w+\s*\{`)
	topMessage   = regexp.MustCompile(`(?m)^message\s+(\w+)\s*\{`)
	packageLine  = regexp.MustCompile(`(?m)^\s*package\s+[\w.]+\s*;`)
	notNamed     = map[string]bool{"double": true, "float": true, "int32": true, "int64": true, "uint32": true, "uint64": true, "sint32": true, "sint64": true, "fixed32": true, "fixed64": true, "sfixed32": true, "sfixed64": true, "bool": true, "string": true, "bytes": true, "map": true, "group": true}
	mutationsOps = []struct {
		name string
		op   func(src, code []byte, syntax string) []byte
	}{
		{"drop_semicolon", func(src, code []byte, _ string) []byte {
			m := fieldLine.FindSubmatchIndex(code)
			if m == nil || code[m[12]] != ';' {
				return nil
			}
			return splice(src, m[12], m[13], "")
		}},
		{"dup_number", dupNumber},
		{"undefined_type", func(src, code []byte, _ string) []byte {
			for _, m := range fieldLine.FindAllSubmatchIndex(code, -1) {
				if !notNamed[string(code[m[6]:m[7]])] {
					return splice(src, m[6], m[7], "UndefinedTypeXyz")
				}
			}
			return nil
		}},
		{"reserved_number", func(src, code []byte, _ string) []byte {
			if m := fieldLine.FindSubmatchIndex(code); m != nil {
				return splice(src, m[10], m[11], "19000")
			}
			return nil
		}},
		{"unclosed", func(src, code []byte, _ string) []byte {
			if i := bytes.LastIndexByte(code, '}'); i >= 0 {
				return splice(src, i, i+1, "")
			}
			return nil
		}},
		{"enum_collision", appendText("\nenum DsNegA { DS_NEG_COLLIDE = 0; }\nenum DsNegB { DS_NEG_COLLIDE = 0; }\n", "")},
		{"json_clash", appendText("\nmessage DsNegJson { int32 foo_bar = 1; int32 fooBar = 2; }\n", syntaxProto2)},
		{"unknown_edition", func(src, code []byte, _ string) []byte {
			m := syntaxLine.FindSubmatchIndex(code)
			if m == nil {
				return nil
			}
			return splice(src, m[2], m[5]+1, `edition = "2025"`)
		}},
		{"dup_message", func(src, code []byte, _ string) []byte {
			m := topMessage.FindSubmatchIndex(code)
			if m == nil {
				return nil
			}
			return append(slices.Clone(src), "\nmessage "+string(code[m[2]:m[3]])+" {}\n"...)
		}},
		{"option_unknown", func(src, code []byte, _ string) []byte {
			m := packageLine.FindIndex(code)
			if m == nil {
				return nil
			}
			return splice(src, m[1], m[1], "\noption ds_neg_unknown_option = true;")
		}},
		{"enum_nonzero", appendText("\nenum DsNegZ { DS_NEG_Z_ONE = 1; }\n", syntaxProto2)},
		{"naming_style", appendText("\nmessage ds_neg_bad_style { int32 BadField = 1; }\n", "")},
		{"proto3_required", onlyProto3("\nmessage DsNegReq { required int32 a = 1; }\n")},
		{"proto3_default", onlyProto3("\nmessage DsNegDef { int32 a = 1 [default = 5]; }\n")},
	}
)

// mutants applies each operator to src, matching on a copy whose comments
// and string bodies are blanked so offsets stay valid in src.
func mutants(src []byte) []mutant {
	code := blankComments(src)
	syntax := syntaxProto2
	if m := syntaxLine.FindSubmatch(src); m != nil {
		syntax = string(m[2])
	}
	var out []mutant
	for _, o := range mutationsOps {
		if m := o.op(src, code, syntax); m != nil {
			out = append(out, mutant{op: o.name, src: m})
		}
	}
	return out
}

func splice(src []byte, i, j int, with string) []byte {
	return slices.Concat(src[:i], []byte(with), src[j:])
}

func appendText(text, unless string) func(src, code []byte, syntax string) []byte {
	return func(src, _ []byte, syntax string) []byte {
		if syntax == unless {
			return nil
		}
		return append(slices.Clone(src), text...)
	}
}

func onlyProto3(text string) func(src, code []byte, syntax string) []byte {
	return func(src, _ []byte, syntax string) []byte {
		if syntax != syntaxProto3 {
			return nil
		}
		return append(slices.Clone(src), text...)
	}
}

// dupNumber gives the second field of the first message holding two fields
// of different numbers the first one's number.
func dupNumber(src, code []byte, _ string) []byte {
	for _, blk := range messageOpen.FindAllIndex(code, -1) {
		start, depth, i := blk[1], 1, blk[1]
		for ; i < len(code) && depth > 0; i++ {
			switch code[i] {
			case '{':
				depth++
			case '}':
				depth--
			}
		}
		ms := fieldLine.FindAllSubmatchIndex(code[start:i], 2)
		if len(ms) < 2 {
			continue
		}
		a, c := ms[0], ms[1]
		first := code[start+a[10] : start+a[11]]
		if bytes.Equal(first, code[start+c[10]:start+c[11]]) {
			continue
		}
		return splice(src, start+c[10], start+c[11], string(first))
	}
	return nil
}

func blankComments(src []byte) []byte {
	out := slices.Clone(src)
	for i := 0; i < len(src); {
		switch {
		case bytes.HasPrefix(src[i:], []byte("//")):
			j := bytes.IndexByte(src[i:], '\n')
			if j < 0 {
				j = len(src) - i
			}
			for k := i; k < i+j; k++ {
				out[k] = ' '
			}
			i += j
		case bytes.HasPrefix(src[i:], []byte("/*")):
			j := bytes.Index(src[i+2:], []byte("*/"))
			end := len(src)
			if j >= 0 {
				end = i + 2 + j + 2
			}
			for k := i; k < end; k++ {
				if out[k] != '\n' {
					out[k] = ' '
				}
			}
			i = end
		case src[i] == '"' || src[i] == '\'':
			q, j := src[i], i+1
			for j < len(src) && src[j] != q && src[j] != '\n' {
				if src[j] == '\\' {
					j++
				}
				j++
			}
			for k := i + 1; k < min(j, len(src)); k++ {
				out[k] = ' '
			}
			i = j + 1
		default:
			i++
		}
	}
	return out
}
