package rundir_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cplieger/deadset/internal/rundir"
	"pgregory.net/rapid"
)

// localPath draws a relative path that stays inside the directory it is
// relative to, spelled with the "." and ".." segments, doubled and trailing
// slashes a caller may write.
func localPath() *rapid.Generator[string] {
	return rapid.Custom(func(t *rapid.T) string {
		var segments []string
		depth := 0
		for range rapid.IntRange(1, 6).Draw(t, "segments") {
			segment := rapid.SampledFrom([]string{"app", "go.work", "consumer one", "é", ".", "..", ""}).Draw(t, "segment")
			switch {
			case segment == ".." && depth == 0:
				segment = "."
			case segment == "..":
				depth--
			case segment != "." && segment != "":
				depth++
			}
			segments = append(segments, segment)
		}
		joined := strings.Join(segments, "/")
		if !filepath.IsLocal(joined) {
			return "."
		}
		return joined
	})
}

// TestWriteScopeAdmitsEveryLocalPath draws scopes whose every path is local and
// checks each document written: the published schema admits it, it names the
// target and every consumer drawn, and every path it names is clean and is the
// working directory or beneath it.
func TestWriteScopeAdmitsEveryLocalPath(t *testing.T) {
	schema := publishedScopeSchema(t)
	rapid.Check(t, func(rt *rapid.T) {
		scope := rundir.Scope{
			Root:   root,
			Target: rundir.Module{ID: rapid.SampledFrom([]string{"", "example.com/app"}).Draw(rt, "target id"), Path: localPath().Draw(rt, "target")},
		}
		if rapid.Bool().Draw(rt, "workspace") {
			scope.Workspace = localPath().Draw(rt, "workspace path")
		}
		for i := range rapid.IntRange(0, 3).Draw(rt, "consumers") {
			scope.Consumers = append(scope.Consumers, rundir.Module{Path: localPath().Draw(rt, "consumer "+string(rune('a'+i)))})
		}

		dir, err := rundir.Create(filepath.Join(t.TempDir(), "run"))
		if err != nil {
			rt.Fatalf("Setup: create the run directory: %v", err)
		}
		if err := dir.WriteScope(&scope); err != nil {
			rt.Fatalf("WriteScope(%+v) = %v, want it written", scope, err)
		}
		written, err := os.ReadFile(dir.Scope())
		if err != nil {
			rt.Fatalf("read %s: %v", dir.Scope(), err)
		}
		document, _ := decoded(rt, dir.Scope(), written).(map[string]any)
		if found := violations(rt, schema, document, ""); len(found) > 0 {
			rt.Fatalf("WriteScope(%+v) wrote\n%s\nwhich %s refuses: %q", scope, written, scopeSchema, found)
		}

		paths := []any{document["target"].(map[string]any)["path"]}
		consumers, _ := document["consumers"].([]any)
		if len(consumers) != len(scope.Consumers) {
			rt.Fatalf("WriteScope(%+v) wrote %d consumers, want %d", scope, len(consumers), len(scope.Consumers))
		}
		for _, consumer := range consumers {
			paths = append(paths, consumer.(map[string]any)["path"])
		}
		if workspace, named := document["workspace"]; named {
			paths = append(paths, workspace)
		}
		for _, named := range paths {
			p, _ := named.(string)
			if filepath.Clean(p) != p || (p != root && !strings.HasPrefix(p, root+"/")) {
				rt.Fatalf("WriteScope(%+v) wrote the path %q, want a clean path at or beneath %s", scope, p, root)
			}
		}
	})
}
