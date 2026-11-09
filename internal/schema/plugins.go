package schema

import (
	"errors"
	"io/fs"
	"path"
	"slices"
	"strings"

	"github.com/cplieger/deadset-spec/v8/report"
	"github.com/cplieger/deadset-spec/v8/vocab"
)

// otherPlugin is the token a plugin name no generator or token matches reads
// as, schemas.18.
const otherPlugin = "other"

// coverage is what the generation configuration says of one schema file.
type coverage struct {
	// unknown is schemas.15: the run cannot account for the file's
	// generation in full.
	unknown bool
	// http is schemas.11: a plugin of the file's configuration serves its
	// services over HTTP.
	http bool
}

// genConfig is one generation configuration and the files it covers.
type genConfig struct {
	dir     string
	plugins []string
	unknown bool
}

// generation reads the generation configuration of every schema file:
// proto.plugins where it names one, else the buf.gen.yaml files above it.
func (r *reading) generation() {
	var configs []genConfig
	if len(r.in.Settings.Plugins) > 0 {
		configs = append(configs, genConfig{dir: ".", plugins: r.in.Settings.Plugins})
	} else {
		for _, name := range r.genFiles {
			configs = append(configs, r.genConfig(name))
		}
	}
	unknown := r.unknownModules()
	for _, f := range r.files {
		f.gen = r.coverageOf(f.rel, configs)
		f.gen.unknown = f.gen.unknown || slices.ContainsFunc(unknown, func(dir string) bool { return under(f.rel, dir) })
	}
}

// coverageOf is what the configurations covering a file say of it; a file
// none covers is one whose generation is unknown.
func (r *reading) coverageOf(rel string, configs []genConfig) *coverage {
	c, covered := &coverage{}, false
	for i := range configs {
		if !under(rel, configs[i].dir) {
			continue
		}
		covered = true
		for _, p := range configs[i].plugins {
			c.http = c.http || slices.Contains(vocab.HTTPExposurePlugins(), p)
			c.unknown = c.unknown || !r.analyzes(p)
		}
		c.unknown = c.unknown || configs[i].unknown
	}
	c.unknown = c.unknown || !covered
	return c
}

// genConfig reads one buf.gen.yaml. A file outside the subset, or of a
// version the reader does not know, is a generation-config-unread note and
// configures its module as though nothing did.
func (r *reading) genConfig(name string) genConfig {
	c := genConfig{dir: path.Dir(name), unknown: true}
	doc, err := r.readYAML(name)
	if err == nil {
		if v := doc.scalar("version"); v != "v1" && v != "v2" {
			err = &unreadError{construct: "the version " + v}
		}
	}
	if err != nil {
		r.unread(name, "the generation configuration is not read: "+construct(err))
		return c
	}
	c.unknown = false
	for _, p := range doc.list("plugins") {
		plugin := pluginName(p)
		c.plugins = append(c.plugins, plugin)
		r.checkOut(name, plugin, p.scalar("out"))
	}
	for _, input := range doc.list("inputs") {
		c.unknown = c.unknown || input.scalar("module") != ""
	}
	return c
}

// unknownModules is the directory of every buf.yaml declaring a module name,
// which schemas.15 reads as a module others may depend on, and of every buf
// file outside the subset, with schemas.17's note.
func (r *reading) unknownModules() []string {
	var dirs []string
	for _, name := range r.bufFiles {
		doc, err := r.bufModule(name)
		if err != nil {
			r.unread(name, "the module configuration is not read: "+construct(err))
			dirs = append(dirs, path.Dir(name))
			continue
		}
		modules := doc.list("modules")
		if doc.scalar("name") != "" || slices.ContainsFunc(modules, func(m *yamlNode) bool { return m.scalar("name") != "" }) {
			dirs = append(dirs, path.Dir(name))
		}
	}
	return dirs
}

// construct is what an unread buf file's error names.
func construct(err error) string {
	if unread, ok := errors.AsType[*unreadError](err); ok {
		return unread.Error()
	}
	return err.Error()
}

// checkOut is schemas.17's last sentence: the out directory of a plugin of an
// analyzed language holds no file of that language.
func (r *reading) checkOut(config, plugin, out string) {
	language, analyzed := pluginLanguage(plugin)
	if !analyzed || !slices.Contains(r.in.Analyzed, language) || out == "" {
		return
	}
	row, _ := vocab.LanguageRowOf(language)
	dir := path.Join(path.Dir(config), out)
	found := false
	_ = fs.WalkDir(r.tree, dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && row.Sources(d.Name()) {
			found = true
			return fs.SkipAll
		}
		return nil
	})
	if !found {
		r.unread(dir, "the out directory "+dir+" of "+plugin+" in "+config+" holds no generated file")
	}
}

func (r *reading) unread(at, message string) {
	r.notes = append(r.notes, report.Note{Kind: report.NoteKindGenerationConfigUnread, Path: at, Key: string(vocab.KeyProtoPlugins), Message: message})
}

// analyzes reports whether the run reads what a plugin writes.
func (r *reading) analyzes(plugin string) bool {
	language, known := pluginLanguage(plugin)
	return known && slices.Contains(r.in.Analyzed, language)
}

// pluginLanguage is the language a generator row of the plugin writes.
func pluginLanguage(plugin string) (vocab.Language, bool) {
	rows := vocab.GeneratorRows()
	for i := range rows {
		if rows[i].Plugin == plugin {
			return rows[i].Language, true
		}
	}
	return "", false
}

// pluginName is schemas.18: the name a buf.gen.yaml plugin entry is matched
// under.
func pluginName(entry *yamlNode) string {
	var name string
	switch {
	case entry.child("local") != nil && !entry.child("local").isScalar:
		parts := entry.strings("local")
		if len(parts) > 2 && parts[0] == "go" && (parts[1] == "run" || parts[1] == "tool") {
			name = parts[2]
		}
	case entry.scalar("local") != "":
		name = entry.scalar("local")
	case entry.scalar("remote") != "":
		name = remoteName(entry.scalar("remote"))
	case entry.scalar("plugin") != "":
		name = remoteName(entry.scalar("plugin"))
	case entry.scalar("name") != "":
		name = entry.scalar("name")
	case entry.scalar("protoc_builtin") != "":
		name = entry.scalar("protoc_builtin")
	}
	return matchPlugin(path.Base(strings.SplitN(name, "@", 2)[0]))
}

// remoteName is the name of a buf.build/<org>/<name> remote, its version
// dropped.
func remoteName(ref string) string {
	ref, _, _ = strings.Cut(ref, ":")
	return path.Base(ref)
}

// matchPlugin is the plugin or token of proto.plugins a name stands for.
func matchPlugin(name string) string {
	short := strings.TrimPrefix(name, "protoc-gen-")
	for _, p := range vocab.ProtoPlugins() {
		if p == name || p == "protoc-gen-"+short || strings.ReplaceAll(p, "-", "_") == short {
			return p
		}
	}
	return otherPlugin
}
