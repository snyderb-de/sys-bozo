package system

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestManagerStatusRendersAptOnlyForInjectedDebianFamilyFacts(t *testing.T) {
	tests := []struct {
		name  string
		facts Facts
		want  []string
	}{
		{
			name:  "ubuntu",
			facts: Facts{OS: "linux", OSID: "ubuntu", AptCachePath: "/usr/bin/apt-cache"},
			want:  []string{"nix: missing", "home-manager: missing", "topgrade: missing", "apt-cache: /usr/bin/apt-cache"},
		},
		{
			name:  "fedora",
			facts: Facts{OS: "linux", OSID: "fedora", AptCachePath: "/usr/bin/apt-cache"},
			want:  []string{"nix: missing", "home-manager: missing", "topgrade: missing", "dnf: missing", "sudo: missing"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.facts.ManagerStatus(); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %#v want %#v", got, tt.want)
			}
		})
	}
}

func TestGitDirtyStatusDistinguishesUnavailableFromClean(t *testing.T) {
	if count, unavailable := gitDirtyStatus(t.TempDir()); count != 0 || !unavailable {
		t.Fatalf("count=%d unavailable=%v", count, unavailable)
	}
}

func TestLocalAuditTreatsSSHConfigCopyAsManaged(t *testing.T) {
	home := t.TempDir()
	dotfiles := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("DOTFILES_REPO", dotfiles)

	sshConfig := "Include ~/.config/ssh/config.d/private.conf\n\nHost github.com\n  User git\n"
	sourcePath := filepath.Join(dotfiles, "configs", "ssh", "config")
	if err := os.MkdirAll(filepath.Dir(sourcePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sourcePath, []byte(sshConfig), 0o644); err != nil {
		t.Fatal(err)
	}

	userSSHConfig := filepath.Join(home, ".ssh", "config")
	if err := os.MkdirAll(filepath.Dir(userSSHConfig), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(userSSHConfig, []byte(sshConfig), 0o600); err != nil {
		t.Fatal(err)
	}

	item := findAuditItem(t, LocalAudit(), "ssh config")
	if !item.OK {
		t.Fatalf("expected ssh config to be managed, got %q", item.Detail)
	}
	if item.Detail != "activation-managed copy" {
		t.Fatalf("unexpected ssh config detail: %q", item.Detail)
	}
}

func TestLocalAuditExplainsUnmanagedSSHConfig(t *testing.T) {
	home := t.TempDir()
	dotfiles := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("DOTFILES_REPO", dotfiles)

	sourcePath := filepath.Join(dotfiles, "configs", "ssh", "config")
	if err := os.MkdirAll(filepath.Dir(sourcePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sourcePath, []byte("Include ~/.config/ssh/config.d/private.conf\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	userSSHConfig := filepath.Join(home, ".ssh", "config")
	if err := os.MkdirAll(filepath.Dir(userSSHConfig), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(userSSHConfig, []byte("Host local\n  HostName example.test\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	item := findAuditItem(t, LocalAudit(), "ssh config")
	if item.OK {
		t.Fatal("expected divergent ssh config to fail audit")
	}
	if item.Description == "" || item.Fix == "" {
		t.Fatalf("expected ssh audit item to include description and fix: %#v", item)
	}
}

func findAuditItem(t *testing.T, items []AuditItem, name string) AuditItem {
	t.Helper()
	for _, item := range items {
		if item.Name == name {
			return item
		}
	}
	t.Fatalf("missing audit item %q", name)
	return AuditItem{}
}

func TestDarwinModuleGenerationReportsSystemGeneration(t *testing.T) {
	root := t.TempDir()
	perUser := filepath.Join(root, "etc", "profiles", "per-user")
	if err := os.MkdirAll(filepath.Join(perUser, "bag"), 0o755); err != nil {
		t.Fatal(err)
	}
	profiles := filepath.Join(root, "nix", "profiles")
	if err := os.MkdirAll(profiles, 0o755); err != nil {
		t.Fatal(err)
	}
	system := filepath.Join(profiles, "system")
	if err := os.Symlink("system-84-link", system); err != nil {
		t.Fatal(err)
	}

	defer swapProfilePaths(perUser, system)()

	got := darwinModuleGeneration("bag")
	if !strings.HasPrefix(got, "gen 84 (nix-darwin) · ") {
		t.Fatalf("darwinModuleGeneration = %q, want gen 84 (nix-darwin) with a date", got)
	}
}

func TestDarwinModuleGenerationEmptyForStandaloneInstall(t *testing.T) {
	root := t.TempDir()
	// No /etc/profiles/per-user/<user>: Home Manager is standalone, so the
	// caller must fall back to `home-manager generations`.
	perUser := filepath.Join(root, "etc", "profiles", "per-user")
	if err := os.MkdirAll(perUser, 0o755); err != nil {
		t.Fatal(err)
	}
	system := filepath.Join(root, "system")
	if err := os.Symlink("system-84-link", system); err != nil {
		t.Fatal(err)
	}

	defer swapProfilePaths(perUser, system)()

	for _, user := range []string{"bag", ""} {
		if got := darwinModuleGeneration(user); got != "" {
			t.Fatalf("darwinModuleGeneration(%q) = %q, want empty", user, got)
		}
	}
}

func swapProfilePaths(perUser, system string) func() {
	prevPerUser, prevSystem := darwinPerUserProfiles, darwinSystemProfile
	darwinPerUserProfiles, darwinSystemProfile = perUser, system
	return func() { darwinPerUserProfiles, darwinSystemProfile = prevPerUser, prevSystem }
}
