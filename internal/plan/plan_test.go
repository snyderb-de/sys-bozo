package plan

import (
	"strings"
	"testing"

	"github.com/snyderb-de/sys-bozo/internal/runner"
)

func TestInstallPlanUsesProfileHostAndExclusions(t *testing.T) {
	p := Install(InstallOptions{
		Profile: "darwin-full",
		Host:    "bagbook",
		Exclude: []string{"fun-tools,zellij"},
	})

	text := strings.Join(p.Lines(), "\n")
	for _, want := range []string{"darwin-full", "bagbook", "fun-tools", "zellij", "catalog/tools.yaml"} {
		if !strings.Contains(text, want) {
			t.Fatalf("install plan missing %q:\n%s", want, text)
		}
	}
	if p.MutatingActions() == 0 {
		t.Fatal("install plan should mark future filesystem work as mutating")
	}
}

func TestUpdatePlanCanSelectSpecificManagers(t *testing.T) {
	p := Update([]string{"brew-update", "brew-autoremove"})
	text := strings.Join(p.Lines(), "\n")

	for _, want := range []string{"brew update", "brew autoremove"} {
		if !strings.Contains(text, want) {
			t.Fatalf("update plan missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "nix flake update") {
		t.Fatalf("update plan should not include nix when not selected:\n%s", text)
	}
}

func TestUpdatePlanDefaultsIncludeTopgradeSweep(t *testing.T) {
	p := Update(nil)
	text := strings.Join(p.Lines(), "\n")

	for _, want := range []string{"Update sys-bozo source", "Rebuild sys-bozo", "topgrade", "--skip-notify", "home_manager", "brew_formula"} {
		if !strings.Contains(text, want) {
			t.Fatalf("default update plan missing %q:\n%s", want, text)
		}
	}
}

func TestUpdatePlanForFedoraHostSkipsBrewAndIncludesDnf(t *testing.T) {
	ctx := runner.Context{
		Hostname:    "butler",
		OS:          "linux",
		OSID:        "fedora",
		SudoBin:     "sudo",
		DnfBin:      "dnf",
		NixBin:      "nix",
		HomeManager: "home-manager",
		Topgrade:    "topgrade",
	}

	p := UpdateForContext(nil, ctx)
	text := strings.Join(p.Lines(), "\n")

	for _, want := range []string{"butler", "fedora-upgrade", "sudo dnf upgrade --refresh -y", "topgrade"} {
		if !strings.Contains(text, want) {
			t.Fatalf("host update plan missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "brew update") {
		t.Fatalf("host update plan should not include brew when brew is unavailable:\n%s", text)
	}
}

func TestUpdatePlanForUnavailableExplicitTaskShowsSkip(t *testing.T) {
	p := UpdateForContext([]string{"brew"}, runner.Context{Hostname: "butler", OS: "linux", OSID: "fedora"})
	text := strings.Join(p.Lines(), "\n")

	for _, want := range []string{"Skip brew", "not available"} {
		if !strings.Contains(text, want) {
			t.Fatalf("explicit unavailable task plan missing %q:\n%s", want, text)
		}
	}
}

func TestMiniUpdatePlanCombinesOverlappingSelectionsOnce(t *testing.T) {
	ctx := runner.Context{OS: "darwin", Hostname: "bags-Mac-mini", Repo: "/fixture", NixBin: "nix", NixStoreBin: "nix-store", SudoBin: "sudo", DarwinRebuild: "darwin-rebuild", HomeManager: "home-manager", BrewBin: "brew", Topgrade: "topgrade", BrewOutdatedCasks: []string{"displaylink", "zed"}}
	p := UpdateForContext([]string{"all", "hmu", "ndu", "topgrade", "brew"}, ctx)
	var commands []string
	for _, action := range p.Actions {
		if len(action.Command) > 0 {
			commands = append(commands, strings.Join(action.Command, " "))
		}
	}
	text := strings.Join(commands, "\n")
	for _, command := range []string{"nix flake update", "sudo -H darwin-rebuild switch", "topgrade ", "brew update", "brew missing"} {
		if strings.Count(text, command) != 1 {
			t.Errorf("wanted exactly one %q in queue:\n%s", command, text)
		}
	}
	for _, duplicate := range []string{"home-manager switch", "brew upgrade", "brew autoremove", "--rollback"} {
		if strings.Contains(text, duplicate) {
			t.Errorf("unexpected duplicate/optional command %q:\n%s", duplicate, text)
		}
	}
	if !(strings.Index(text, "nix flake update") < strings.Index(text, "darwin-rebuild switch") && strings.Index(text, "darwin-rebuild switch") < strings.Index(text, "topgrade ") && strings.Index(text, "topgrade ") < strings.Index(text, "brew missing")) {
		t.Errorf("incorrect recommended order:\n%s", text)
	}
}

func TestPackageSearchPlanDoesNotMutateBeforeApply(t *testing.T) {
	p := PackageSearch("yazi")
	text := strings.Join(p.Lines(), "\n")

	for _, want := range []string{"nix search nixpkgs yazi", "brew search yazi", "catalog/tools.yaml"} {
		if !strings.Contains(text, want) {
			t.Fatalf("package plan missing %q:\n%s", want, text)
		}
	}
	if p.MutatingActions() != 1 {
		t.Fatalf("expected one future catalog/profile edit, got %d", p.MutatingActions())
	}
}

func TestMovePackageVerifiesBeforeRemoval(t *testing.T) {
	p := MovePackage("yazi", "brew", "nix")
	text := strings.Join(p.Lines(), "\n")

	verifyIndex := strings.Index(text, "Verify replacement")
	removeIndex := strings.Index(text, "Offer old provider removal")
	if verifyIndex == -1 || removeIndex == -1 {
		t.Fatalf("move plan missing verify/remove steps:\n%s", text)
	}
	if verifyIndex > removeIndex {
		t.Fatalf("move plan removes before verifying:\n%s", text)
	}
}

func TestTarballDefaultsToLocalOptAndManifest(t *testing.T) {
	p := Tarball("thing", "1.2.3", "")
	text := strings.Join(p.Lines(), "\n")

	for _, want := range []string{"~/.local/opt/thing/1.2.3", "~/.local/bin/", "~/.local/state/sys-bozo/tarballs/thing.json"} {
		if !strings.Contains(text, want) {
			t.Fatalf("tarball plan missing %q:\n%s", want, text)
		}
	}
}
