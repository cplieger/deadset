package config_test

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/cplieger/deadset/v2/internal/config"
)

// isRefusal reports whether err is a configuration the product refuses.
func isRefusal(err error) bool {
	_, refused := errors.AsType[*config.Error](err)
	return refused
}

// writeFile writes one file below dir.
func writeFile(t *testing.T, dir, name string, data []byte) string {
	t.Helper()

	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("Setup: write %s: %v", path, err)
	}
	return path
}

// The repository configuration is read from the target root where it exists and is
// absent otherwise; a central configuration is read from where the invocation names
// it and must exist there.
func TestReadDocuments(t *testing.T) {
	t.Parallel()

	target, elsewhere := t.TempDir(), t.TempDir()
	repository := []byte(`{"target": {"kind": "library"}}`)
	writeFile(t, target, config.RepositoryFile, repository)
	central := writeFile(t, elsewhere, "central.json", []byte(`{}`))

	repo, centralDocument, err := config.ReadDocuments(t.Context(), target, central)
	if err != nil {
		t.Fatalf("ReadDocuments(%s, %s) = error %v", target, central, err)
	}
	if want := filepath.Join(target, config.RepositoryFile); repo.Path != want || !bytes.Equal(repo.Data, repository) {
		t.Errorf("ReadDocuments(%s) repository = %s holding %q, want %s holding %q", target, repo.Path, repo.Data, want, repository)
	}
	if centralDocument.Path != central || string(centralDocument.Data) != "{}" {
		t.Errorf("ReadDocuments(%s) central = %s holding %q, want %s holding {}", central, centralDocument.Path, centralDocument.Data, central)
	}

	repo, centralDocument, err = config.ReadDocuments(t.Context(), elsewhere, "")
	if err != nil {
		t.Fatalf("ReadDocuments(a root with no repository configuration) = error %v", err)
	}
	if repo.Data != nil || repo.Path != filepath.Join(elsewhere, config.RepositoryFile) ||
		centralDocument.Path != "" || centralDocument.Data != nil {
		t.Errorf("ReadDocuments(a root with no repository configuration) = %+v and %+v, want an absent repository configuration and no central one",
			repo, centralDocument)
	}

	if _, _, err := config.ReadDocuments(t.Context(), target, filepath.Join(elsewhere, "missing.json")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("ReadDocuments(a central configuration that does not exist) = error %v, want one wrapping fs.ErrNotExist", err)
	}

	oversized := t.TempDir()
	writeFile(t, oversized, config.RepositoryFile, bytes.Repeat([]byte(" "), 1<<20+1))
	if _, _, err := config.ReadDocuments(t.Context(), oversized, ""); !isRefusal(err) {
		t.Errorf("ReadDocuments(a repository configuration over the size bound) = error %v, want a *config.Error", err)
	}
}

// The repository configuration is read inside the target root alone: a
// symbolic link leading out of it and a file that is not a regular one are not
// read, and a link inside it is.
func TestReadDocuments_readsTheRepositoryConfigurationInsideTheTarget(t *testing.T) {
	t.Parallel()

	repository := []byte(`{"target": {"kind": "library"}}`)
	cases := []struct {
		setup func(t *testing.T, target string)
		name  string
		reads bool
	}{
		{name: "link-out-of-the-target", setup: func(t *testing.T, target string) {
			outside := writeFile(t, t.TempDir(), "deadset.json", repository)
			link(t, outside, filepath.Join(target, config.RepositoryFile))
		}},
		{name: "link-to-an-endless-device", setup: func(t *testing.T, target string) {
			link(t, "/dev/zero", filepath.Join(target, config.RepositoryFile))
		}},
		{name: "named-pipe", setup: func(t *testing.T, target string) {
			if err := syscall.Mkfifo(filepath.Join(target, config.RepositoryFile), 0o600); err != nil {
				t.Fatalf("Setup: %v", err)
			}
		}},
		{name: "link-inside-the-target", reads: true, setup: func(t *testing.T, target string) {
			writeFile(t, target, "shared.json", repository)
			link(t, "shared.json", filepath.Join(target, config.RepositoryFile))
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			target := t.TempDir()
			c.setup(t, target)
			repo, _, err := config.ReadDocuments(t.Context(), target, "")
			switch {
			case c.reads && (err != nil || !bytes.Equal(repo.Data, repository)):
				t.Errorf("ReadDocuments(%s) = %q, error %v, want %q", c.name, repo.Data, err, repository)
			case !c.reads && (err == nil || repo.Data != nil):
				t.Errorf("ReadDocuments(%s) = %q, error %v, want an error and nothing read", c.name, repo.Data, err)
			}
		})
	}
}

// link makes name a symbolic link to target.
func link(t *testing.T, target, name string) {
	t.Helper()

	if err := os.Symlink(target, name); err != nil {
		t.Fatalf("Setup: %v", err)
	}
}
