package smoke_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find repo root")
		}
		dir = parent
	}
}

func TestRequiredProjectFilesExist(t *testing.T) {
	root := repoRoot(t)

	required := []string{
		"README.md",
		"TODO.md",
		"catalog/profiles.yaml",
		"catalog/tools.yaml",
		"catalog/hosts.example.yaml",
		"catalog/secrets.yaml",
		"docs/index.html",
		"docs/install.html",
		"docs/tools.html",
		"docs/styles.css",
	}

	for _, path := range required {
		path := path
		t.Run(path, func(t *testing.T) {
			info, err := os.Stat(filepath.Join(root, path))
			if err != nil {
				t.Fatalf("missing required file: %s", path)
			}
			if info.IsDir() {
				t.Fatalf("required path is a directory, expected file: %s", path)
			}
		})
	}
}

func TestDocsDoNotReferenceOldDotfilesPath(t *testing.T) {
	root := repoRoot(t)

	err := filepath.WalkDir(filepath.Join(root, "docs"), func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".html" {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(data)
		for _, stale := range []string{
			"docs/system",
			"Dotfiles System Atlas",
			"system atlas",
			"two Macs",
		} {
			if strings.Contains(text, stale) {
				t.Fatalf("%s contains stale reference %q", path, stale)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCatalogSketchMentionsCoreProfiles(t *testing.T) {
	root := repoRoot(t)

	data, err := os.ReadFile(filepath.Join(root, "catalog/profiles.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)

	for _, profile := range []string{
		"docs:",
		"shell-lite:",
		"cli-nix:",
		"home-manager:",
		"brew-apps:",
		"darwin-full:",
		"linux-home:",
	} {
		if !strings.Contains(text, profile) {
			t.Fatalf("catalog/profiles.yaml missing profile %s", profile)
		}
	}
}
