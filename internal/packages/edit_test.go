package packages

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveTargetMapsOnlySupportedDestinations(t *testing.T) {
	repo := t.TempDir()
	tests := []struct {
		name     string
		goos     string
		provider Provider
		kind     Kind
		scope    Scope
		want     Target
	}{
		{
			name:     "shared nix",
			goos:     "darwin",
			provider: ProviderNix,
			kind:     KindPackage,
			scope:    ScopeShared,
			want:     Target{Path: filepath.Join(repo, "home/modules/packages.nix"), Assignment: "home.packages", ApplyAction: "hms"},
		},
		{
			name:     "darwin nix",
			goos:     "darwin",
			provider: ProviderNix,
			kind:     KindPackage,
			scope:    ScopePlatform,
			want:     Target{Path: filepath.Join(repo, "home/darwin/default.nix"), Assignment: "home.packages", ApplyAction: "hms"},
		},
		{
			name:     "linux nix",
			goos:     "linux",
			provider: ProviderNix,
			kind:     KindPackage,
			scope:    ScopePlatform,
			want:     Target{Path: filepath.Join(repo, "home/linux/default.nix"), Assignment: "home.packages", ApplyAction: "hms"},
		},
		{
			name:     "brew formula",
			goos:     "darwin",
			provider: ProviderBrew,
			kind:     KindFormula,
			scope:    ScopeShared,
			want:     Target{Path: filepath.Join(repo, "homebrew.nix"), Assignment: "brews", Quoted: true, ApplyAction: "nds"},
		},
		{
			name:     "brew cask",
			goos:     "darwin",
			provider: ProviderBrew,
			kind:     KindCask,
			scope:    ScopeShared,
			want:     Target{Path: filepath.Join(repo, "homebrew.nix"), Assignment: "casks", Quoted: true, ApplyAction: "nds"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveTarget(repo, tt.goos, tt.provider, tt.kind, tt.scope)
			if err != nil || got != tt.want {
				t.Fatalf("target=%#v err=%v want %#v", got, err, tt.want)
			}
		})
	}

	unsupported := []struct {
		goos     string
		provider Provider
		kind     Kind
		scope    Scope
	}{
		{goos: "freebsd", provider: ProviderNix, kind: KindPackage, scope: ScopePlatform},
		{goos: "darwin", provider: ProviderNix, kind: KindPackage, scope: ScopeHost},
		{goos: "darwin", provider: ProviderBrew, kind: KindFormula, scope: ScopePlatform},
		{goos: "darwin", provider: ProviderBrew, kind: KindPackage, scope: ScopeShared},
	}
	for _, tt := range unsupported {
		got, err := ResolveTarget(repo, tt.goos, tt.provider, tt.kind, tt.scope)
		if !errors.Is(err, ErrAmbiguousTarget) || got != (Target{}) {
			t.Fatalf("unsupported target=%#v err=%v", got, err)
		}
	}
}

func TestSectionsReturnsOrderedUniqueNames(t *testing.T) {
	original := []byte("{\n  home.packages = with pkgs; [\n    # Git tooling\n    gh\n\n    # Misc\n    sqlite\n  ];\n}\n")

	got, err := Sections(original, Target{Assignment: "home.packages"})
	if err != nil {
		t.Fatal(err)
	}
	want := []Section{"Git tooling", "Misc"}
	if len(got) != len(want) {
		t.Fatalf("sections=%#v want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sections[%d]=%q want %q", i, got[i], want[i])
		}
	}
}

func TestSectionsFailsClosedOnDuplicateNames(t *testing.T) {
	original := []byte("home.packages = [\n  # Misc\n  one\n  # Misc\n  two\n];\n")

	_, err := Sections(original, Target{Assignment: "home.packages"})
	if !errors.Is(err, ErrAmbiguousTarget) {
		t.Fatalf("err=%v", err)
	}
}

func TestProposeAddPreservesNixFileAndInsertsInSection(t *testing.T) {
	original := []byte("{ pkgs, ... }:\n{\n  home.packages = with pkgs; [\n    # Git tooling\n    gh\n\n    # Misc\n    sqlite\n  ];\n}\n")
	target := Target{Assignment: "home.packages", Quoted: false}
	proposal, err := ProposeAdd(original, target, "Misc", "lazydocker")
	if err != nil {
		t.Fatal(err)
	}
	want := "    # Misc\n    sqlite\n    lazydocker\n"
	if !strings.Contains(string(proposal.Proposed), want) {
		t.Fatalf("%s", proposal.Proposed)
	}
	if !strings.Contains(proposal.Diff, "+    lazydocker") {
		t.Fatalf("diff=%s", proposal.Diff)
	}
	if string(original) == string(proposal.Proposed) {
		t.Fatal("proposal did not change")
	}
}

func TestProposeAddQuotesBrewItemAndRejectsDuplicate(t *testing.T) {
	original := []byte("{\n  casks = [\n    # Core baseline\n    \"raycast\"\n  ];\n}\n")
	target := Target{Assignment: "casks", Quoted: true}
	proposal, err := ProposeAdd(original, target, "Core baseline", "zed")
	if err != nil || !bytes.Contains(proposal.Proposed, []byte(`    "zed"`)) {
		t.Fatalf("proposal=%#v err=%v", proposal, err)
	}
	_, err = ProposeAdd(proposal.Proposed, target, "Core baseline", "zed")
	if !errors.Is(err, ErrAlreadyDeclared) {
		t.Fatalf("err=%v", err)
	}
}

func TestProposeAddRejectsDuplicateDespiteWhitespaceDrift(t *testing.T) {
	original := []byte("home.packages = [\n  # Misc\n      yazi   \n];\n")

	_, err := ProposeAdd(original, Target{Assignment: "home.packages"}, "Misc", "yazi")
	if !errors.Is(err, ErrAlreadyDeclared) {
		t.Fatalf("err=%v", err)
	}
}

func TestProposeAddFailsClosedOnMultipleAssignments(t *testing.T) {
	original := []byte("home.packages = [ ];\nhome.packages = [ ];\n")
	_, err := ProposeAdd(original, Target{Assignment: "home.packages"}, "Misc", "x")
	if !errors.Is(err, ErrAmbiguousTarget) {
		t.Fatalf("err=%v", err)
	}
}

func TestProposeAddFailsClosedOnMalformedOrMissingTarget(t *testing.T) {
	tests := []struct {
		name     string
		original string
	}{
		{name: "missing assignment", original: "other.packages = [\n  # Misc\n  x\n];\n"},
		{name: "missing opening bracket", original: "home.packages = with pkgs;\n  # Misc\n  x\n];\n"},
		{name: "comparison instead of assignment", original: "home.packages == [\n  # Misc\n  x\n];\n"},
		{name: "opening bracket only in comment", original: "home.packages = with pkgs; # [\n  # Misc\n  x\n];\n"},
		{name: "missing closing bracket", original: "home.packages = [\n  # Misc\n  x\n"},
		{name: "inline list", original: "home.packages = [ ];\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ProposeAdd([]byte(tt.original), Target{Assignment: "home.packages"}, "Misc", "yazi")
			if !errors.Is(err, ErrAmbiguousTarget) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestProposeAddReturnsSectionNotFound(t *testing.T) {
	original := []byte("home.packages = [\n  # Core\n  gh\n];\n")

	_, err := ProposeAdd(original, Target{Assignment: "home.packages"}, "Misc", "yazi")
	if !errors.Is(err, ErrSectionNotFound) {
		t.Fatalf("err=%v", err)
	}
}

func TestProposeAddPreservesBlankEdgeCRLFHashesAndInputOwnership(t *testing.T) {
	original := []byte("{\r\n  casks = [\r\n    # Core\r\n    \"raycast\"\r\n\r\n    # Misc\r\n    \"zed\"\r\n  ];\r\n}\r\n")
	wantProposed := "{\r\n  casks = [\r\n    # Core\r\n    \"raycast\"\r\n    \"ghostty\"\r\n\r\n    # Misc\r\n    \"zed\"\r\n  ];\r\n}\r\n"

	proposal, err := ProposeAdd(original, Target{Path: "homebrew.nix", Assignment: "casks", Quoted: true}, "Core", "ghostty")
	if err != nil {
		t.Fatal(err)
	}
	if string(proposal.Proposed) != wantProposed {
		t.Fatalf("proposed:\n%q\nwant:\n%q", proposal.Proposed, wantProposed)
	}
	wantOriginalHash := sha256.Sum256(original)
	wantProposedHash := sha256.Sum256([]byte(wantProposed))
	if proposal.OriginalHash != wantOriginalHash || proposal.ProposedHash != wantProposedHash {
		t.Fatalf("hashes original=%x proposed=%x", proposal.OriginalHash, proposal.ProposedHash)
	}
	if !strings.HasPrefix(proposal.Diff, "--- original\n+++ proposed\n@@ -1,9 +1,10 @@\n") {
		t.Fatalf("diff=%q", proposal.Diff)
	}
	if strings.Count(proposal.Diff, "@@") != 2 || !strings.Contains(proposal.Diff, "+    \"ghostty\"\n") {
		t.Fatalf("diff=%q", proposal.Diff)
	}

	original[0] = '!'
	if proposal.Original[0] != '{' || proposal.Proposed[0] != '{' {
		t.Fatalf("proposal retained caller-owned bytes: original=%q proposed=%q", proposal.Original[0], proposal.Proposed[0])
	}
}
