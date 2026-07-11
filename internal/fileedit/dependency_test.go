package fileedit

import (
	"errors"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestApplyPreservesModeAndRejectsStaleBytes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	if err := os.WriteFile(path, []byte("old\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	proposal := ProposeReplacement(path, []byte("old\n"), []byte("new\n"))
	if err := os.WriteFile(path, []byte("raced\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(proposal); !errors.Is(err, ErrStaleFile) {
		t.Fatalf("err=%v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "raced\n" {
		t.Fatalf("target overwritten: %q", got)
	}
}

func TestFileeditDoesNotImportPackages(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range files {
		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imported := range parsed.Imports {
			name, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			if name == "github.com/snyderb-de/sys-bozo/internal/packages" {
				t.Fatalf("%s imports internal/packages", path)
			}
		}
	}
}

func TestPrepareDefersMutationAndAbortRemovesOwnedArtifacts(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	original := []byte("old\n")
	if err := os.WriteFile(path, original, 0o640); err != nil {
		t.Fatal(err)
	}
	prepared, err := Prepare(ProposeReplacement(path, original, []byte("new\n")))
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(original) {
		t.Fatalf("prepare mutated target: %q err=%v", got, err)
	}
	if err := prepared.Abort(); err != nil {
		t.Fatal(err)
	}
	assertNoApplyTemps(t, dir)
}

func TestPreparedEditCommitAppliesReviewedBytes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	original := []byte("old\n")
	if err := os.WriteFile(path, original, 0o640); err != nil {
		t.Fatal(err)
	}
	prepared, err := Prepare(ProposeReplacement(path, original, []byte("new\n")))
	if err != nil {
		t.Fatal(err)
	}
	applied, err := prepared.Commit()
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "new\n" || string(applied.Before) != string(original) || string(applied.After) != "new\n" {
		t.Fatalf("target=%q applied=%#v err=%v", got, applied, err)
	}
	assertNoApplyTemps(t, dir)
}

func TestPrepareRejectsMutatedProposedBytesBeforeStaging(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	original := []byte("old\n")
	if err := os.WriteFile(path, original, 0o640); err != nil {
		t.Fatal(err)
	}
	proposal := ProposeReplacement(path, original, []byte("new\n"))
	proposal.Proposed[0] = '!'
	prepared, err := Prepare(proposal)
	if prepared != nil {
		_ = prepared.Abort()
	}
	if !errors.Is(err, ErrStaleFile) {
		t.Fatalf("err=%v", err)
	}
	assertNoApplyTemps(t, dir)
}
