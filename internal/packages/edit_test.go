package packages

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"os"
	"os/exec"
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

func TestResolveEditorTargetOwnsExplicitFallbackPolicy(t *testing.T) {
	repo := t.TempDir()
	tests := []struct {
		name     string
		goos     string
		hostname string
		provider Provider
		kind     Kind
		scope    Scope
		want     Target
	}{
		{"known shared nix", "darwin", "mac", ProviderNix, KindPackage, ScopeShared, Target{Path: filepath.Join(repo, "home/modules/packages.nix"), Assignment: "home.packages", ApplyAction: "hms"}},
		{"darwin host nix", "darwin", "mac", ProviderNix, KindPackage, ScopeHost, Target{Path: filepath.Join(repo, "hosts/mac/darwin.nix"), Assignment: "home.packages", ApplyAction: "nds"}},
		{"linux host nix", "linux", "box", ProviderNix, KindPackage, ScopeHost, Target{Path: filepath.Join(repo, "hosts/box/home.nix"), Assignment: "home.packages", ApplyAction: "hms"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveEditorTarget(repo, tt.goos, tt.hostname, tt.provider, tt.kind, tt.scope)
			if err != nil || got != tt.want {
				t.Fatalf("target=%#v err=%v want=%#v", got, err, tt.want)
			}
		})
	}
	for _, scope := range []Scope{ScopePlatform, ScopeHost} {
		for _, kind := range []Kind{KindFormula, KindCask} {
			got, err := ResolveEditorTarget(repo, "darwin", "mac", ProviderBrew, kind, scope)
			if !errors.Is(err, ErrUnsupportedTarget) || got != (Target{}) {
				t.Fatalf("brew scope=%q kind=%q target=%#v err=%v", scope, kind, got, err)
			}
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

func TestSectionsSynthesizesUncategorizedForUnsectionedMappedShapes(t *testing.T) {
	tests := []struct {
		name     string
		original string
		target   Target
		item     string
		wantLine string
	}{
		{
			name:     "empty Darwin home packages",
			original: "{ pkgs, ... }:\n{\n  home.packages = with pkgs; [\n  ];\n}\n",
			target:   Target{Assignment: "home.packages"},
			item:     "lazydocker",
			wantLine: "    lazydocker\n",
		},
		{
			name:     "unsectioned Linux home packages",
			original: "{ pkgs, ... }:\n{\n  home.packages = with pkgs; [\n    coreutils\n  ];\n}\n",
			target:   Target{Assignment: "home.packages"},
			item:     "yazi",
			wantLine: "    yazi\n",
		},
		{
			name:     "unsectioned Brew formulae",
			original: "{\n  brews = [\n    \"mas\"\n  ];\n}\n",
			target:   Target{Assignment: "brews", Quoted: true},
			item:     "wget",
			wantLine: "    \"wget\"\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sections, err := Sections([]byte(tt.original), tt.target)
			if err != nil || len(sections) != 1 || sections[0] != "Uncategorized" {
				t.Fatalf("sections=%#v err=%v", sections, err)
			}
			proposal, err := ProposeAdd([]byte(tt.original), tt.target, "Uncategorized", tt.item)
			if err != nil || !bytes.Contains(proposal.Proposed, []byte(tt.wantLine)) {
				t.Fatalf("proposal=%q err=%v", proposal.Proposed, err)
			}
		})
	}
}

func TestSectionsDoesNotSynthesizeUncategorizedWhenRealSectionsExist(t *testing.T) {
	original := []byte("home.packages = [\n  # Misc\n  yazi\n];\n")

	sections, err := Sections(original, Target{Assignment: "home.packages"})
	if err != nil || len(sections) != 1 || sections[0] != "Misc" {
		t.Fatalf("sections=%#v err=%v", sections, err)
	}
}

func TestSectionsIgnoresHeadingsInsideStringsAndBlockComments(t *testing.T) {
	original := []byte("home.packages = [\n  # Misc\n  \"text [\n  # Fake string section\n  ] text\"\n  /* [\n  # Fake comment section\n  ] */\n  yazi\n];\n")

	sections, err := Sections(original, Target{Assignment: "home.packages"})
	if err != nil || len(sections) != 1 || sections[0] != "Misc" {
		t.Fatalf("sections=%#v err=%v", sections, err)
	}
	proposal, err := ProposeAdd(original, Target{Assignment: "home.packages"}, "Misc", "lazydocker")
	if err != nil {
		t.Fatal(err)
	}
	wantTail := "  ] */\n  yazi\n  lazydocker\n];\n"
	if !bytes.Contains(proposal.Proposed, []byte(wantTail)) {
		t.Fatalf("proposal=%q", proposal.Proposed)
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

func TestProposeAddDetectsNormalizedDuplicatesAcrossListContent(t *testing.T) {
	tests := []struct {
		name     string
		original string
		target   Target
		section  string
		item     string
	}{
		{
			name:     "bare Nix item with trailing comment",
			original: "home.packages = [\n  # Misc\n  yazi # file manager\n];\n",
			target:   Target{Assignment: "home.packages"},
			section:  "Misc",
			item:     "yazi",
		},
		{
			name:     "quoted Brew item with trailing comment",
			original: "casks = [\n  # Core\n  \"zed\" # editor\n];\n",
			target:   Target{Assignment: "casks", Quoted: true},
			section:  "Core",
			item:     "zed",
		},
		{
			name:     "item after assignment opening",
			original: "home.packages = [ yazi\n  # Misc\n  ripgrep\n];\n",
			target:   Target{Assignment: "home.packages"},
			section:  "Misc",
			item:     "yazi",
		},
		{
			name:     "unsupported inline multi-item reports duplicate first",
			original: "home.packages = [ yazi ripgrep ];\n",
			target:   Target{Assignment: "home.packages"},
			section:  "Uncategorized",
			item:     "yazi",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ProposeAdd([]byte(tt.original), tt.target, tt.section, tt.item)
			if !errors.Is(err, ErrAlreadyDeclared) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestProposeAddFailsClosedOnMultipleAssignments(t *testing.T) {
	original := []byte("home.packages = [ ];\nhome.packages = [ ];\n")
	_, err := ProposeAdd(original, Target{Assignment: "home.packages"}, "Misc", "x")
	if !errors.Is(err, ErrAmbiguousTarget) {
		t.Fatalf("err=%v", err)
	}
}

func TestProposeAddIgnoresAssignmentTextInsideStringsAndComments(t *testing.T) {
	original := []byte("text = ''\nhome.packages = [\n'';\n/*\nhome.packages = [\n*/\nhome.packages = [\n  # Misc\n  yazi\n];\n")

	proposal, err := ProposeAdd(original, Target{Assignment: "home.packages"}, "Misc", "lazydocker")
	if err != nil || !bytes.Contains(proposal.Proposed, []byte("  lazydocker\n")) {
		t.Fatalf("proposal=%q err=%v", proposal.Proposed, err)
	}
}

func TestProposeAddKeepsIndentedStringEscapesLexicallyIsolated(t *testing.T) {
	escapes := []struct {
		name string
		line string
	}{
		{name: "escaped apostrophe", line: "  ''' escaped apostrophe"},
		{name: "escaped dollar", line: "  ''$ escaped dollar"},
		{name: "escaped backslash", line: "  ''\\[ escaped bracket"},
	}

	for _, tt := range escapes {
		t.Run(tt.name, func(t *testing.T) {
			original := []byte("text = ''\n" + tt.line + "\nhome.packages = [\n  # Fake\n  fake\n];\n'';\nhome.packages = [\n  # Misc\n  yazi\n];\n")

			sections, err := Sections(original, Target{Assignment: "home.packages"})
			if err != nil || len(sections) != 1 || sections[0] != "Misc" {
				t.Fatalf("sections=%#v err=%v", sections, err)
			}
			proposal, err := ProposeAdd(original, Target{Assignment: "home.packages"}, "Misc", "lazydocker")
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(proposal.Proposed, []byte("  # Misc\n  yazi\n  lazydocker\n];\n")) {
				t.Fatalf("proposal=%q", proposal.Proposed)
			}
			if strings.Count(string(proposal.Proposed), "lazydocker") != 1 || !bytes.Contains(proposal.Proposed, []byte("  # Fake\n  fake\n];\n")) {
				t.Fatalf("fake target was changed: %q", proposal.Proposed)
			}
		})
	}
}

func TestProposeAddRejectsFakeTargetAfterIndentedStringEscape(t *testing.T) {
	original := []byte("text = ''\n  ''$ escaped dollar\nhome.packages = [\n  # Fake\n  fake\n];\n'';\n")

	_, err := ProposeAdd(original, Target{Assignment: "home.packages"}, "Fake", "lazydocker")
	if !errors.Is(err, ErrAmbiguousTarget) {
		t.Fatalf("err=%v", err)
	}
}

func TestProposeAddIgnoresDuplicateTextInsideEscapedIndentedString(t *testing.T) {
	original := []byte("home.packages = [\n  # Misc\n  ''\n  ''$ escaped dollar\n  yazi\n  ''\n];\n")

	proposal, err := ProposeAdd(original, Target{Assignment: "home.packages"}, "Misc", "yazi")
	if err != nil {
		t.Fatal(err)
	}
	wantTail := "  yazi\n  ''\n  yazi\n];\n"
	if !bytes.Contains(proposal.Proposed, []byte(wantTail)) {
		t.Fatalf("proposal=%q", proposal.Proposed)
	}
}

func TestProposeAddFailsClosedOnMalformedOrMissingTarget(t *testing.T) {
	tests := []struct {
		name     string
		original string
	}{
		{name: "missing assignment", original: "other.packages = [\n  # Misc\n  x\n];\n"},
		{name: "missing opening bracket", original: "home.packages = with pkgs;\n  # Misc\n  x\n];\n"},
		{name: "opening bracket on later line", original: "home.packages =\n[\n  # Misc\n  x\n];\n"},
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

func TestProposeAddTracksListDepthOutsideStringsAndComments(t *testing.T) {
	t.Run("nested list fails closed", func(t *testing.T) {
		original := []byte("home.packages = [\n  # Misc\n  yazi\n  [ ripgrep ]\n];\n")

		_, err := ProposeAdd(original, Target{Assignment: "home.packages"}, "Misc", "lazydocker")
		if !errors.Is(err, ErrAmbiguousTarget) {
			t.Fatalf("err=%v", err)
		}
	})

	t.Run("quoted and commented brackets are ignored", func(t *testing.T) {
		original := []byte("home.packages = builtins.trace \"[not the list]\" [\n  # Misc\n  yazi # ignored [ and ]\n]; # ignored [\n")

		proposal, err := ProposeAdd(original, Target{Assignment: "home.packages"}, "Misc", "lazydocker")
		if err != nil || !bytes.Contains(proposal.Proposed, []byte("  lazydocker\n")) {
			t.Fatalf("proposal=%q err=%v", proposal.Proposed, err)
		}
	})

	t.Run("malformed closing suffix fails closed", func(t *testing.T) {
		original := []byte("home.packages = [\n  # Misc\n  yazi\n] + other;\n")

		_, err := ProposeAdd(original, Target{Assignment: "home.packages"}, "Misc", "lazydocker")
		if !errors.Is(err, ErrAmbiguousTarget) {
			t.Fatalf("err=%v", err)
		}
	})

	t.Run("single quoted pseudo string fails closed", func(t *testing.T) {
		original := []byte("home.packages = '[not Nix]' [\n  # Misc\n  yazi\n];\n")

		_, err := ProposeAdd(original, Target{Assignment: "home.packages"}, "Misc", "lazydocker")
		if !errors.Is(err, ErrAmbiguousTarget) {
			t.Fatalf("err=%v", err)
		}
	})
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
	if strings.Count(proposal.Diff, "@@") != 2 || !strings.Contains(proposal.Diff, "+    \"ghostty\"\r\n") {
		t.Fatalf("diff=%q", proposal.Diff)
	}

	original[0] = '!'
	if proposal.Original[0] != '{' || proposal.Proposed[0] != '{' {
		t.Fatalf("proposal retained caller-owned bytes: original=%q proposed=%q", proposal.Original[0], proposal.Proposed[0])
	}
}

func TestProposalDiffAppliesToExactProposedBytes(t *testing.T) {
	patchCommand, err := exec.LookPath("patch")
	if err != nil {
		t.Skip("patch is unavailable")
	}
	tests := []struct {
		name       string
		original   string
		wantMarker bool
	}{
		{
			name:       "CRLF",
			original:   "home.packages = [\r\n  # Misc\r\n  yazi\r\n];\r\n",
			wantMarker: false,
		},
		{
			name:       "missing final newline",
			original:   "home.packages = [\n  # Misc\n  yazi\n];",
			wantMarker: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proposal, err := ProposeAdd([]byte(tt.original), Target{Assignment: "home.packages"}, "Misc", "lazydocker")
			if err != nil {
				t.Fatal(err)
			}
			if tt.name == "CRLF" && !strings.Contains(proposal.Diff, "  yazi\r\n+  lazydocker\r\n") {
				t.Fatalf("diff does not preserve CRLF hunk content: %q", proposal.Diff)
			}
			marker := "\\ No newline at end of file\n"
			if strings.Contains(proposal.Diff, marker) != tt.wantMarker {
				t.Fatalf("marker presence=%t want %t diff=%q", strings.Contains(proposal.Diff, marker), tt.wantMarker, proposal.Diff)
			}

			path := filepath.Join(t.TempDir(), "fixture.nix")
			if err := os.WriteFile(path, []byte(tt.original), 0o600); err != nil {
				t.Fatal(err)
			}
			command := exec.Command(patchCommand, "-s", path)
			command.Stdin = strings.NewReader(proposal.Diff)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("patch failed: %v\n%s\ndiff=%q", err, output, proposal.Diff)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, proposal.Proposed) {
				t.Fatalf("patched=%q want proposed=%q", got, proposal.Proposed)
			}
		})
	}
}
