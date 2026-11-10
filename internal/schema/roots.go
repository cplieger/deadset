package schema

import (
	"cmp"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/cplieger/deadset-spec/v8/report"
	"github.com/cplieger/deadset-spec/v8/vocab"
	"github.com/cplieger/deadset/v2/internal/schema/proto"
)

// The buf configuration files the reader reads.
const (
	bufYAML     = "buf.yaml"
	bufWorkYAML = "buf.work.yaml"
	bufGenYAML  = "buf.gen.yaml"
)

// The buf.yaml versions whose module layout differs from version 1's.
const (
	bufV1beta1 = "v1beta1"
	bufV2      = "v2"
)

// schemaFile is one schema file of the target.
type schemaFile struct {
	unit *proto.Unit
	locs map[string]*proto.Location
	gen  *coverage
	// rel is the target-relative path, importPath the path from the first
	// import root holding the file, else rel, or for an upstream copy no
	// root holds that another file shadows, its upstream path.
	rel, importPath string
	src             []byte
	decls           []*decl
}

// load finds every schema file and buf configuration of the target, decides
// the import roots and links the files.
func (r *reading) load() error {
	if err := r.discover(); err != nil {
		return err
	}
	r.bufFiles = slices.DeleteFunc(r.bufFiles, r.noWorkspace)
	r.built = unbuilt{FS: r.tree, dropped: r.dropUnbuilt()}
	if len(r.files) == 0 {
		return r.noProgram()
	}
	roots, err := r.importRoots()
	if err != nil {
		return err
	}
	fsys := make([]fs.FS, 0, len(roots))
	for _, root := range roots {
		sub, subErr := fs.Sub(r.built, root.dir)
		if subErr != nil {
			return fmt.Errorf("schema: %w", subErr)
		}
		if root.copies != nil {
			sub = copiesOnly{FS: sub, copies: root.copies}
		}
		fsys = append(fsys, sub)
	}
	placed, err := r.place(roots)
	if err != nil {
		return err
	}
	byName, outside := proto.Link(fsys, proto.Files{
		Names: placed.names, Detached: placed.detached, Strict: r.defaultRoots(),
		Same: func(root int, name string) string { return placed.held[path.Join(roots[root].dir, name)] },
	})
	r.units = append(slices.Collect(maps.Values(byName)), outside...)
	for _, f := range r.files {
		r.take(f, placed, byName, outside, roots)
	}
	r.withheldNote()
	return nil
}

// take reads one schema file's link result, or its shadowing, into its unit,
// its note and the target's files not read cleanly.
func (r *reading) take(f *schemaFile, placed *placement, byName map[string]*proto.Unit, outside []*proto.Unit, roots []importRoot) {
	read, shadowed := placed.shadowing[f]
	if !shadowed {
		f.unit = byName[f.importPath]
		if i, ok := placed.detachedAt[f]; ok {
			f.unit = outside[i]
		}
	}
	if !clean(f.unit) {
		r.unclean[f.rel] = r.why(f.unit, read)
	}
	if shadowed {
		r.notes = append(r.notes, report.Note{
			Kind: report.NoteKindSchemaUnparsed, Path: f.rel,
			Message: "the import path " + f.importPath + " of the schema file names " + read + ", so the compiler reads that file and none of this file's declarations is published",
		})
		return
	}
	r.linked(f, roots)
}

// clean reports whether a schema file's unit is read cleanly, schemas.4: the
// file is the one its import path names, so it has a unit, and it links
// with every import and every name resolved. Every other schema file may
// use any declaration of the target.
func clean(u *proto.Unit) bool {
	return u != nil && u.Err == nil && len(u.Missing) == 0 && len(u.Unresolved) == 0
}

// uncleanFile is why a schema file is not read cleanly, for the target's note,
// and whether the default roots' remedy answers it.
type uncleanFile struct {
	why           string
	defaultRemedy bool
}

// why is why a schema file whose unit is u is not read cleanly: shadowed by
// the file read, an import no root holds, or a failure to compile.
func (r *reading) why(u *proto.Unit, read string) uncleanFile {
	switch {
	case read != "":
		return uncleanFile{why: "shadowed by " + read}
	case u == nil:
		return uncleanFile{why: "it cannot be read"}
	case len(u.Missing) > 0 && (u.Err == nil || r.defaultRoots()):
		subject, verb := imports(u.Missing)
		return uncleanFile{why: subject + " " + verb + " no file of the import roots", defaultRemedy: r.defaultRoots()}
	}
	return uncleanFile{why: "it does not compile"}
}

// imports is the subject and verb a note names missing imports with.
func imports(missing []string) (subject, verb string) {
	if len(missing) == 1 {
		return "the import " + missing[0], "names"
	}
	return "the imports " + strings.Join(missing, ", "), "name"
}

// placement is where the schema files sit among the import roots: the
// import path of each file a root holds by its target-relative path, those
// import paths in file order, the files no root holds by their index in
// detached, and each shadowed file with where the file its import path names
// lies.
type placement struct {
	detachedAt map[*schemaFile]int
	shadowing  map[*schemaFile]string
	held       map[string]string
	names      []string
	detached   []proto.Detached
}

// place sets every schema file's import path and reads the source of each
// that is not shadowed. A file no root holds is shadowed too where a root,
// or the include directory of the compiler, holds a file of its import path
// or, for an upstream copy, of its upstream path, which every import of
// that path reads.
func (r *reading) place(roots []importRoot) (*placement, error) {
	p := &placement{detachedAt: map[*schemaFile]int{}, shadowing: map[*schemaFile]string{}, held: map[string]string{}}
	for _, f := range r.files {
		var held bool
		f.importPath, held = importPath(roots, f.rel)
		if read := r.shadowedBy(roots, f.importPath, held, f.rel); read != "" {
			p.shadowing[f] = read
			continue
		}
		if at, copied := r.upstream(f); copied && !held {
			if read := r.shadowedBy(roots, at, false, ""); read != "" {
				f.importPath, p.shadowing[f] = at, read
				continue
			}
		}
		var err error
		if f.src, err = fs.ReadFile(r.tree, f.rel); err != nil {
			return nil, fmt.Errorf("schema: %w", err)
		}
		if held {
			p.held[f.rel] = f.importPath
			p.names = append(p.names, f.importPath)
		} else {
			p.detachedAt[f] = len(p.detached)
			p.detached = append(p.detached, proto.Detached{Name: f.importPath, Src: f.src})
		}
	}
	return p, nil
}

// shadowedBy is where the file an import path names lies, under an import
// root or, for a file no root holds, in the compiler's include directory,
// when that is not the schema file at rel; empty when the path names that
// file or none.
func (r *reading) shadowedBy(roots []importRoot, importPath string, held bool, rel string) string {
	read := r.resolved(roots, importPath)
	switch {
	case read != "" && read != rel && held:
		return read + " under an earlier import root"
	case read != "" && read != rel:
		return read + " under an import root"
	case !held && proto.Embedded(importPath):
		return importPath + " in the compiler's own include directory"
	}
	return ""
}

// resolved is the file an import path names: the first import root holding a
// file of that path, as protoc searches its --proto_path.
func (r *reading) resolved(roots []importRoot, importPath string) string {
	for _, root := range roots {
		if !root.holds(importPath) {
			continue
		}
		at := path.Join(root.dir, importPath)
		if info, err := fs.Stat(r.built, at); err == nil && !info.IsDir() {
			return at
		}
	}
	return ""
}

// importRoot is one import root: its target-relative directory and, for the
// include root of upstream copies, the import paths of those copies, the
// only files it holds.
type importRoot struct {
	copies map[string]bool
	dir    string
}

func (root importRoot) holds(importPath string) bool {
	return root.copies == nil || root.copies[importPath]
}

// copiesOnly is an include root's tree holding only its copies.
type copiesOnly struct {
	fs.FS
	copies map[string]bool
}

func (c copiesOnly) Open(name string) (fs.File, error) {
	if !c.copies[name] {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	return c.FS.Open(name)
}

// noProgram is schemas.1's target that holds no schema file, naming the files
// the proto row reads a program from.
func (r *reading) noProgram() *SetupError {
	row, _ := vocab.LanguageRowOf(vocab.Proto)
	return &SetupError{Class: vocab.SetupNoProgram, Detail: fmt.Sprintf(
		"the target %s holds no %s file outside the directories named %s and those its buf.yaml modules leave out; run from the directory that holds the schema files, or name the languages to analyze in %s",
		r.in.Target, strings.Join(row.Extensions, " or "), strings.Join(row.SkippedDirectories, ", "), vocab.KeyAnalysisLanguages,
	)}
}

// discover walks the target for its schema files and buf files, skipping the
// directories the proto row skips.
func (r *reading) discover() error {
	row, _ := vocab.LanguageRowOf(vocab.Proto)
	err := fs.WalkDir(r.tree, ".", func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case d.IsDir() && p != "." && row.Skips(d.Name()):
			return fs.SkipDir
		case d.IsDir():
			return nil
		case row.Sources(d.Name()):
			r.files = append(r.files, &schemaFile{rel: p})
		case d.Name() == bufYAML || d.Name() == bufWorkYAML:
			r.bufFiles = append(r.bufFiles, p)
		case d.Name() == bufGenYAML:
			r.genFiles = append(r.genFiles, p)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("schema: walk the target: %w", err)
	}
	return nil
}

// noWorkspace reports whether name is a buf.work.yaml listing no directory
// but its own, which buf reads as no buf.work.yaml at all
// (https://buf.build/docs/configuration/v1/buf-work-yaml/), so it names no
// root.
func (r *reading) noWorkspace(name string) bool {
	if path.Base(name) != bufWorkYAML {
		return false
	}
	if _, err := r.bufModule(name); err != nil {
		return false
	}
	return !slices.ContainsFunc(r.declared(name), func(m bufModuleFiles) bool { return m.dir != path.Dir(name) })
}

// defaultRoots reports whether neither proto.import_paths nor a buf file
// names the import roots, so they are buf's for a directory with no
// buf.yaml.
func (r *reading) defaultRoots() bool {
	return len(r.in.Settings.ImportPaths) == 0 && len(r.bufFiles) == 0
}

// importRoots is schemas.3: proto.import_paths, else the buf module roots,
// else the include root of each upstream copy and the target root.
func (r *reading) importRoots() ([]importRoot, error) {
	var roots []importRoot
	var dirs []string
	switch {
	case len(r.in.Settings.ImportPaths) > 0:
		for _, p := range r.in.Settings.ImportPaths {
			info, err := fs.Stat(r.tree, path.Clean(p))
			if err != nil || !info.IsDir() {
				return nil, &SetupError{Class: vocab.SetupMissingImportPath, Detail: fmt.Sprintf(
					"proto.import_paths names %s, which is no directory of the target; name a directory that holds schema files", p,
				)}
			}
			dirs = append(dirs, path.Clean(p))
		}
	case len(r.bufFiles) > 0:
		dirs = r.moduleRoots()
	default:
		roots, dirs = r.includeRoots(), []string{"."}
	}
	for _, dir := range unique(dirs) {
		roots = append(roots, importRoot{dir: dir})
	}
	return roots, nil
}

// includeRoots is the directory above each schema file that is a copy of an
// upstream schema below it, schemas.7, as the include path a build passes to
// protoc for the copy, holding the copies below it alone; a deeper directory
// first, so each copy takes the import path of its own.
func (r *reading) includeRoots() []importRoot {
	copies := map[string]map[string]bool{}
	for _, f := range r.files {
		if at, ok := r.upstream(f); ok {
			dir := strings.TrimSuffix(f.rel, "/"+at)
			if copies[dir] == nil {
				copies[dir] = map[string]bool{}
			}
			copies[dir][at] = true
		}
	}
	dirs := slices.SortedFunc(maps.Keys(copies), func(a, b string) int {
		return cmp.Or(cmp.Compare(strings.Count(b, "/"), strings.Count(a, "/")), strings.Compare(a, b))
	})
	roots := make([]importRoot, 0, len(dirs))
	for _, dir := range dirs {
		roots = append(roots, importRoot{dir: dir, copies: copies[dir]})
	}
	return roots
}

// upstream is the upstream path of a schema file that is a copy below a
// directory, schemas.7.
func (r *reading) upstream(f *schemaFile) (string, bool) {
	at, ok := copyPath(f.rel, func() string {
		src, err := fs.ReadFile(r.tree, f.rel)
		if err != nil {
			return ""
		}
		return proto.Package(f.rel, src)
	})
	return at, ok && at != f.rel
}

// unique is roots with every root after its first place dropped.
func unique(roots []string) []string {
	seen := map[string]bool{}
	return slices.DeleteFunc(roots, func(root string) bool {
		again := seen[root]
		seen[root] = true
		return again
	})
}

// moduleRoots is the directory of every module the buf files declare. A
// directory a buf.work.yaml lists that holds a buf.yaml stands for that
// file's own modules, as buf builds the workspace.
func (r *reading) moduleRoots() []string {
	var roots []string
	for _, name := range r.bufFiles {
		for _, m := range r.declared(name) {
			if path.Base(name) == bufWorkYAML && slices.Contains(r.bufFiles, path.Join(m.dir, bufYAML)) {
				continue
			}
			roots = append(roots, m.dir)
		}
	}
	return roots
}

// bufModuleFiles is the file set of one buf module: the schema files below
// dir, less those below an exclude and, where includes is not empty, those
// below none of the includes. Every path is target-relative.
type bufModuleFiles struct {
	dir                string
	includes, excludes []string
}

// declared is the modules one buf file declares, as buf computes them: a
// buf.work.yaml's directories, a version 2 buf.yaml's modules, a v1beta1
// buf.yaml's build roots, else the directory of the buf.yaml; each path
// relative to the file's directory. A file outside the subset declares its
// directory and excludes nothing.
func (r *reading) declared(name string) []bufModuleFiles {
	dir := path.Dir(name)
	doc, err := r.bufModule(name)
	if err != nil {
		return []bufModuleFiles{{dir: dir}}
	}
	join := func(paths []string) []string {
		joined := make([]string, 0, len(paths))
		for _, p := range paths {
			joined = append(joined, path.Join(dir, p))
		}
		return joined
	}
	build := doc.child("build")
	var mods []bufModuleFiles
	switch {
	case path.Base(name) == bufWorkYAML:
		for _, d := range doc.strings("directories") {
			mods = append(mods, bufModuleFiles{dir: d})
		}
	case doc.scalar("version") == bufV2:
		for _, m := range doc.list("modules") {
			mods = append(mods, bufModuleFiles{dir: m.scalar("path"), includes: join(m.strings("includes")), excludes: join(m.strings("excludes"))})
		}
	case doc.scalar("version") == bufV1beta1:
		for _, root := range build.strings("roots") {
			mods = append(mods, bufModuleFiles{dir: root, excludes: join(build.strings("excludes"))})
		}
	}
	mods = slices.DeleteFunc(mods, func(m bufModuleFiles) bool { return m.dir == "" })
	if len(mods) == 0 {
		mods = []bufModuleFiles{{dir: ".", excludes: join(build.strings("excludes"))}}
	}
	for i := range mods {
		mods[i].dir = path.Join(dir, mods[i].dir)
	}
	return mods
}

// dropUnbuilt drops every schema file that each buf.yaml module whose
// directory holds it leaves out of its build, and returns the paths it
// dropped; a file no module's directory holds stays.
func (r *reading) dropUnbuilt() map[string]bool {
	dropped := map[string]bool{}
	var mods []bufModuleFiles
	for _, name := range r.bufFiles {
		if path.Base(name) == bufYAML {
			mods = append(mods, r.declared(name)...)
		}
	}
	r.files = slices.DeleteFunc(r.files, func(f *schemaFile) bool {
		if builds(mods, f.rel) {
			return false
		}
		dropped[f.rel] = true
		return true
	})
	return dropped
}

// builds reports whether a module whose directory holds the target-relative
// file rel builds it, or no module's directory holds it.
func builds(mods []bufModuleFiles, rel string) bool {
	held := false
	for _, m := range mods {
		if !under(rel, m.dir) {
			continue
		}
		held = true
		excluded := slices.ContainsFunc(m.excludes, func(dir string) bool { return under(rel, dir) })
		included := len(m.includes) == 0 || slices.ContainsFunc(m.includes, func(dir string) bool { return under(rel, dir) })
		if included && !excluded {
			return true
		}
	}
	return !held
}

// unbuilt is a tree less the schema files no buf module builds, so no import
// root holds one.
type unbuilt struct {
	fs.FS
	dropped map[string]bool
}

func (u unbuilt) Open(name string) (fs.File, error) {
	if u.dropped[name] {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	return u.FS.Open(name)
}

// under reports whether the target-relative file rel lies below dir.
func under(rel, dir string) bool { return dir == "." || strings.HasPrefix(rel, dir+"/") }

// bufModule is a buf.yaml or buf.work.yaml read once in the subset: the
// module roots and the generation configuration read the same document. A
// file of a version the reader does not know is outside the subset.
func (r *reading) bufModule(name string) (*yamlNode, error) {
	if held, ok := r.bufDocs[name]; ok {
		return held.doc, held.err
	}
	doc, err := r.readYAML(name)
	if err == nil {
		err = bufVersion(name, doc.scalar("version"))
	}
	r.bufDocs[name] = bufDoc{doc: doc, err: err}
	return doc, err
}

// bufVersion refuses a version the file's kind does not take: a buf.work.yaml
// takes v1 alone, https://buf.build/docs/configuration/v1/buf-work-yaml/.
func bufVersion(name, version string) error {
	known := []string{"", bufV1beta1, "v1", bufV2}
	if path.Base(name) == bufWorkYAML {
		known = []string{"v1"}
	}
	switch {
	case slices.Contains(known, version):
		return nil
	case version == "":
		return &unreadError{construct: "no version"}
	}
	return &unreadError{construct: "the version " + version}
}

// importPath is a file's path from the first root holding it, else its
// target-relative path with held false.
func importPath(roots []importRoot, rel string) (at string, held bool) {
	for _, root := range roots {
		if root.dir == "." {
			return rel, true
		}
		if rest, found := strings.CutPrefix(rel, root.dir+"/"); found && root.holds(rest) {
			return rest, true
		}
	}
	return rel, false
}

// linked reads a file's link result: an error is schemas.4's note, a missing
// import schemas.5's, or with the default roots schemas.3's.
func (r *reading) linked(f *schemaFile, roots []importRoot) {
	u := f.unit
	if u != nil && u.Err != nil && len(u.Missing) > 0 && r.defaultRoots() {
		subject, verb := imports(u.Missing)
		r.notes = append(r.notes, report.Note{
			Kind: report.NoteKindUnresolvedImport, Path: f.rel, Key: string(vocab.KeyProtoImportPaths),
			Message: subject + " " + verb + " no file of the import roots, so the schema file does not compile and none of its declarations is published: " +
				firstError(u.Err) + " " + defaultRemedy + ".",
		})
		f.unit = nil
		return
	}
	if u == nil || u.Err != nil {
		message := "the file cannot be read"
		if u != nil {
			message = firstError(u.Err)
		}
		r.notes = append(r.notes, report.Note{
			Kind: report.NoteKindSchemaUnparsed, Path: f.rel,
			Message: "the schema file does not compile, so none of its declarations is published: " + message,
		})
		f.unit = nil
		return
	}
	f.locs = map[string]*proto.Location{}
	for _, l := range u.File.Locations {
		key := pathKey(l.Path)
		if _, held := f.locs[key]; !held {
			f.locs[key] = l
		}
	}
	if len(u.Missing) > 0 {
		dirs := make([]string, 0, len(roots))
		for _, root := range roots {
			dirs = append(dirs, root.dir)
		}
		subject, verb := imports(u.Missing)
		they, theirs := "it", "its root"
		if len(u.Missing) > 1 {
			they, theirs = "they", "their roots"
		}
		r.notes = append(r.notes, report.Note{
			Kind: report.NoteKindUnresolvedImport, Path: f.rel, Key: string(vocab.KeyProtoImportPaths),
			Message: subject + " " + verb + " no file of the import roots searched (" + strings.Join(dirs, ", ") +
				"), so the declarations " + they + " would supply types to are held live; name " + theirs + " in proto.import_paths",
		})
	}
}

// defaultRemedy is how a target the default roots cannot read says where
// imports resolve from.
const defaultRemedy = "Set proto.import_paths, or add a buf.yaml, to say where imports resolve from"

// withheldNote is the target's note when a schema file it holds is not read
// cleanly: that file may use any declaration, so none is reported unused.
// It lists each such file with why, and where one has an import the
// default roots do not hold, the default roots' remedy.
func (r *reading) withheldNote() {
	if len(r.unclean) == 0 {
		return
	}
	var listed []string
	remedy := false
	for _, f := range r.files {
		if held, ok := r.unclean[f.rel]; ok {
			listed = append(listed, f.rel+" ("+held.why+")")
			remedy = remedy || held.defaultRemedy
		}
	}
	files, uses, pointer := "1 schema file", "it uses", "its note names why."
	if len(listed) > 1 {
		files, uses, pointer = strconv.Itoa(len(listed))+" schema files", "they use", "the note on each names why."
	}
	ending := strings.ToUpper(pointer[:1]) + pointer[1:]
	if remedy {
		ending = defaultRemedy + "; " + pointer
	}
	r.notes = append(r.notes, report.Note{
		Kind: report.NoteKindSchemaFindingsWithheld, Path: ".",
		Message: "schema findings withheld: " + files + " could not be read cleanly, and any declaration of the target may be one only " + uses + ": " +
			strings.Join(listed, ", ") + ". " + ending,
	})
}

// firstError is the first error protoc would print for a file, with its
// 1-based line and column.
func firstError(err error) string {
	var es proto.Errors
	if errors.As(err, &es) && len(es) > 0 {
		return es[0].Error()
	}
	return err.Error()
}

func pathKey(p []int32) string {
	var b strings.Builder
	for i, x := range p {
		if i > 0 {
			b.WriteByte('.')
		}
		fmt.Fprint(&b, x)
	}
	return b.String()
}
