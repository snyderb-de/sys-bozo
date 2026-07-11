package catalog

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

const realToolsFixture = "# catalog\ngroups:\n  fun-tools:\n    tools: [cmatrix]\ntools:\n  atuin:\n    category: shell\n    defaultProfiles: [linux-home]\n    nix: atuin\n    brew: atuin\n    config:\n      sync: local-only\n  iterm2:\n    category: gui\n    defaultProfiles: [darwin-full]\n    brewCask: iterm2\n    platforms: [macos]\n  fun-tools:\n    category: group\n    defaultProfiles: [linux-home]\n    optional: true\n"
const realHostsFixture = "hosts:\n  butler:\n    os: linux\n    arch: x86_64\n    profile: linux-home\n    user: bag\n    dotfiles_flake_key: \"bag@butler\"\n    include: [lazygit]\n    exclude: [iterm2]\n"

func TestLoadPreservesCatalogVocabularyAndHostProfile(t *testing.T) {
	repo := fixtureCatalogRepo(t, realToolsFixture, realHostsFixture)
	store, err := Load(repo)
	if err != nil {
		t.Fatal(err)
	}

	tool := store.Tools["atuin"]
	if tool.ProviderIDs["nix"] != "atuin" || tool.ProviderIDs["brew"] != "atuin" || tool.ConfigSync != "local-only" {
		t.Fatalf("tool=%#v", tool)
	}
	if len(tool.DefaultProfiles) != 1 || tool.DefaultProfiles[0] != "linux-home" {
		t.Fatalf("default profiles=%#v", tool.DefaultProfiles)
	}
	host := store.Hosts["butler"]
	if host.Profile != "linux-home" || host.User != "bag" || host.Include[0] != "lazygit" || host.Exclude[0] != "iterm2" {
		t.Fatalf("host=%#v", host)
	}
}

func TestLoadRejectsMalformedOrDuplicateSemanticKeys(t *testing.T) {
	for _, tools := range []string{
		"tools: [",
		"tools:\n  yazi:\n    nix: one\n    nix: two\n",
		"tools:\n  yazi:\n    config:\n      sync: local-only\n      sync: remote\n",
		"tools: {}\ntools: {}\n",
	} {
		repo := fixtureCatalogRepo(t, tools, "hosts: {}\n")
		if _, err := Load(repo); err == nil || !errors.Is(err, ErrInvalidCatalog) || (tools != "tools: [" && !errors.Is(err, ErrDuplicateKey)) {
			t.Fatalf("Load(%q) error=%v", tools, err)
		}
	}
}

func TestLoadPreservesOriginalBytesAndNodeGraphs(t *testing.T) {
	repo := fixtureCatalogRepo(t, realToolsFixture, realHostsFixture)
	store, err := Load(repo)
	if err != nil {
		t.Fatal(err)
	}

	if string(store.toolsBytes) != realToolsFixture || string(store.hostsBytes) != realHostsFixture {
		t.Fatal("original catalog bytes were not preserved")
	}
	if store.toolsDoc == nil || store.toolsDoc.Kind != yaml.DocumentNode || store.hostsDoc == nil || store.hostsDoc.Kind != yaml.DocumentNode {
		t.Fatalf("document nodes not preserved: tools=%#v hosts=%#v", store.toolsDoc, store.hostsDoc)
	}
}

func TestLoadCheckedInCatalogUsesExistingVocabulary(t *testing.T) {
	repo := filepath.Clean(filepath.Join("..", ".."))
	store, err := Load(repo)
	if err != nil {
		t.Fatal(err)
	}

	if got := store.Tools["iterm2"].ProviderIDs["brewCask"]; got != "iterm2" {
		t.Fatalf("iterm2 brewCask=%q", got)
	}
	if got := store.Tools["fun-tools"].Category; got != "group" {
		t.Fatalf("fun-tools category=%q", got)
	}
	if got := store.Hosts["butler"]; got.OS != "linux" || got.Arch != "x86_64" || got.Profile != "linux-home" {
		t.Fatalf("butler=%#v", got)
	}
}

func TestLoadRejectsWrongCatalogTypes(t *testing.T) {
	tests := []struct {
		name  string
		tools string
		hosts string
	}{
		{name: "root sequence", tools: "- tools\n", hosts: "hosts: {}\n"},
		{name: "missing tools", tools: "groups: {}\n", hosts: "hosts: {}\n"},
		{name: "tools sequence", tools: "tools: []\n", hosts: "hosts: {}\n"},
		{name: "tool scalar", tools: "tools:\n  yazi: nope\n", hosts: "hosts: {}\n"},
		{name: "provider number", tools: "tools:\n  yazi:\n    nix: 7\n", hosts: "hosts: {}\n"},
		{name: "profiles scalar", tools: "tools:\n  yazi:\n    defaultProfiles: linux-home\n", hosts: "hosts: {}\n"},
		{name: "profile number item", tools: "tools:\n  yazi:\n    defaultProfiles: [7]\n", hosts: "hosts: {}\n"},
		{name: "config sequence", tools: "tools:\n  yazi:\n    config: []\n", hosts: "hosts: {}\n"},
		{name: "optional string", tools: "tools:\n  yazi:\n    optional: \"true\"\n", hosts: "hosts: {}\n"},
		{name: "missing hosts", tools: "tools: {}\n", hosts: "other: {}\n"},
		{name: "host list", tools: "tools: {}\n", hosts: "hosts: []\n"},
		{name: "host scalar", tools: "tools: {}\n", hosts: "hosts:\n  butler: linux\n"},
		{name: "host profile boolean", tools: "tools: {}\n", hosts: "hosts:\n  butler:\n    profile: true\n"},
		{name: "include scalar", tools: "tools: {}\n", hosts: "hosts:\n  butler:\n    include: yazi\n"},
		{name: "multiple documents", tools: "tools: {}\n---\ntools: {}\n", hosts: "hosts: {}\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := fixtureCatalogRepo(t, tt.tools, tt.hosts)
			if _, err := Load(repo); err == nil || !errors.Is(err, ErrInvalidCatalog) {
				t.Fatalf("Load() error=%v, want ErrInvalidCatalog", err)
			}
		})
	}
}

func TestLoadRejectsSymlinkAndNonRegularCatalogFiles(t *testing.T) {
	t.Run("symlink", func(t *testing.T) {
		repo := fixtureCatalogRepo(t, "tools: {}\n", "hosts: {}\n")
		toolsPath := filepath.Join(repo, "catalog", "tools.yaml")
		target := filepath.Join(repo, "elsewhere.yaml")
		if err := os.WriteFile(target, []byte("tools: {}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(toolsPath); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, toolsPath); err != nil {
			t.Fatal(err)
		}

		if _, err := Load(repo); err == nil || !errors.Is(err, ErrNonRegularCatalogFile) {
			t.Fatalf("Load() error=%v, want ErrNonRegularCatalogFile", err)
		}
	})

	t.Run("directory", func(t *testing.T) {
		repo := fixtureCatalogRepo(t, "tools: {}\n", "hosts: {}\n")
		hostsPath := filepath.Join(repo, "catalog", "hosts.yaml")
		if err := os.Remove(hostsPath); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(hostsPath, 0o700); err != nil {
			t.Fatal(err)
		}

		if _, err := Load(repo); err == nil || !errors.Is(err, ErrNonRegularCatalogFile) {
			t.Fatalf("Load() error=%v, want ErrNonRegularCatalogFile", err)
		}
	})
}

func fixtureCatalogRepo(t *testing.T, tools, hosts string) string {
	t.Helper()
	repo := t.TempDir()
	for relative, content := range map[string]string{"catalog/tools.yaml": tools, "catalog/hosts.yaml": hosts} {
		path := filepath.Join(repo, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	return repo
}
