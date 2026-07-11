package packages

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/snyderb-de/sys-bozo/internal/fileedit"
)

func TestProposeReplacementRetainsTargetMetadata(t *testing.T) {
	target := Target{Path: "/fixture/packages.nix", Assignment: "home.packages", ApplyAction: "hms", NixInput: "nixpkgs"}
	proposal := ProposeReplacement(target, []byte("old\n"), []byte("new\n"))
	if proposal.Target != target {
		t.Fatalf("target=%#v want %#v", proposal.Target, target)
	}
}

func TestApplyConversionPreservesCommittedEditOnError(t *testing.T) {
	wantErr := errors.New("cleanup failed")
	target := Target{Path: "/fixture/packages.nix", ApplyAction: "hms"}
	got, err := finishApply(target, fileedit.AppliedEdit{Path: target.Path, Before: []byte("old\n"), After: []byte("new\n")}, wantErr)
	if !errors.Is(err, wantErr) || got.Target != target || got.Path != target.Path || string(got.Before) != "old\n" || string(got.After) != "new\n" {
		t.Fatalf("edit=%#v err=%v", got, err)
	}
}

func TestApplyAndProposeRevertRetainTargetMetadata(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "packages.nix")
	original := []byte("old\n")
	if err := os.WriteFile(path, original, 0o640); err != nil {
		t.Fatal(err)
	}
	target := Target{Path: path, Assignment: "home.packages", ApplyAction: "hms", NixInput: "nixpkgs"}
	applied, err := Apply(ProposeReplacement(target, original, []byte("new\n")))
	if err != nil {
		t.Fatal(err)
	}
	if applied.Target != target {
		t.Fatalf("applied target=%#v want %#v", applied.Target, target)
	}
	revert, err := ProposeRevert(applied)
	if err != nil {
		t.Fatal(err)
	}
	if revert.Target != target || !bytes.Equal(revert.Proposed, original) {
		t.Fatalf("revert=%#v", revert)
	}
}
