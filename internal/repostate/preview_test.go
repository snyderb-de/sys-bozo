package repostate

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadPreviewShowsStagedAndUnstagedDiffs(t *testing.T) {
	repo := initTempRepo(t)
	path := filepath.Join(repo, "tracked.txt")
	if err := os.WriteFile(path, []byte("staged\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitFixture(t, repo, "add", "--", "tracked.txt")
	if err := os.WriteFile(path, []byte("unstaged\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	entry := mustInspect(t, repo).Entries[0]

	preview := LoadPreview(context.Background(), ExecRunner{}, RealFileSystem{}, repo, "git", entry)
	if preview.Kind != PreviewDiff || !strings.Contains(preview.Staged, "staged") || !strings.Contains(preview.Unstaged, "unstaged") {
		t.Fatalf("preview=%#v", preview)
	}
}

func TestLoadPreviewLabelsBinaryUntrackedContent(t *testing.T) {
	repo := initTempRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "binary.dat"), []byte{'a', 0, 'b'}, 0o600); err != nil {
		t.Fatal(err)
	}
	entries := mustInspect(t, repo).Entries
	preview := LoadPreview(context.Background(), ExecRunner{}, RealFileSystem{}, repo, "git", entries[0])
	if preview.Kind != PreviewBinary {
		t.Fatalf("preview=%#v", preview)
	}
}

func TestLoadPreviewTreatsSelectedPathLiterally(t *testing.T) {
	repo := initTempRepo(t)
	writeFixture(t, repo, "*.txt", "base\n")
	gitFixture(t, repo, "--literal-pathspecs", "add", "--", "*.txt")
	gitFixture(t, repo, "commit", "-qm", "literal fixture")
	writeFixture(t, repo, "*.txt", "selected change\n")
	writeFixture(t, repo, "tracked.txt", "unselected change\n")
	gitFixture(t, repo, "add", "-u")
	writeFixture(t, repo, "*.txt", "selected unstaged\n")
	writeFixture(t, repo, "tracked.txt", "unselected unstaged\n")
	preview := LoadPreview(context.Background(), ExecRunner{}, RealFileSystem{}, repo, "git", Entry{Path: "*.txt"})
	if preview.Kind != PreviewDiff || !strings.Contains(preview.Staged, "selected change") || !strings.Contains(preview.Unstaged, "selected unstaged") || strings.Contains(preview.Staged+preview.Unstaged, "unselected") {
		t.Fatalf("preview includes unselected content or misses selected content: %#v", preview)
	}
}
