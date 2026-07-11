package repostate

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestValidateFingerprintsRejectsSameStatusAfterContentChange(t *testing.T) {
	repo := initTempRepo(t)
	path := filepath.Join(repo, "tracked.txt")
	if err := os.WriteFile(path, []byte("first\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	entries := mustInspect(t, repo).Entries
	fingerprints, err := FingerprintEntries(context.Background(), ExecRunner{}, RealFileSystem{}, repo, "git", entries)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("other\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateFingerprints(context.Background(), ExecRunner{}, RealFileSystem{}, repo, "git", fingerprints); !errors.Is(err, ErrStaleStatus) {
		t.Fatalf("err=%v", err)
	}
}

func initTempRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.name", "Fixture"},
		{"config", "user.email", "fixture@example.invalid"},
		{"config", "commit.gpgsign", "false"},
	} {
		gitFixture(t, repo, args...)
	}
	path := filepath.Join(repo, "tracked.txt")
	if err := os.WriteFile(path, []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitFixture(t, repo, "add", "--", "tracked.txt")
	gitFixture(t, repo, "commit", "-qm", "fixture base")
	return repo
}

func gitFixture(t *testing.T, repo string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func mustInspect(t *testing.T, repo string) Status {
	t.Helper()
	status := Inspect(context.Background(), ExecRunner{}, repo, "git")
	if status.Err != nil {
		t.Fatal(status.Err)
	}
	return status
}
