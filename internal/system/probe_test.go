package system

import (
	"os"
	"path/filepath"
	"testing"
)

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
