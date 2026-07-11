package repostate

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestProposeCommitUsesOnlyAndPathBoundary(t *testing.T) {
	entry := Entry{Path: "--odd name", Worktree: StateModified, DisplayFingerprint: sha256.Sum256([]byte("fixture status"))}
	fingerprint := ActionFingerprint{Path: entry.Path, Status: entry.DisplayFingerprint, Kind: FingerprintRegular, Worktree: sha256.Sum256([]byte("fixture bytes")), Mode: 0o600}
	op, err := ProposeAction(ActionRequest{Repo: "/repo", GitBin: "git", Kind: ActionCommit, Message: "fix config", Entries: []Entry{entry}, Fingerprints: []ActionFingerprint{fingerprint}})
	if err != nil {
		t.Fatal(err)
	}
	want := []Command{
		{Name: "git", Args: []string{"add", "--", "--odd name"}},
		{Name: "git", Args: []string{"commit", "--only", "-m", "fix config", "--", "--odd name"}, Interactive: true},
	}
	if !reflect.DeepEqual(op.Commands, want) {
		t.Fatalf("commands=%#v", op.Commands)
	}
}

func TestCommitOperationExcludesUnrelatedStagedPath(t *testing.T) {
	repo := initTempRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "selected.txt"), []byte("selected\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "unrelated.txt"), []byte("unrelated\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitFixture(t, repo, "add", "--", "unrelated.txt")
	status := mustInspect(t, repo)
	var selected Entry
	for _, entry := range status.Entries {
		if entry.Path == "selected.txt" {
			selected = entry
		}
	}
	fingerprints, err := FingerprintEntries(context.Background(), ExecRunner{}, RealFileSystem{}, repo, "git", []Entry{selected})
	if err != nil {
		t.Fatal(err)
	}
	op, err := ProposeAction(ActionRequest{Repo: repo, GitBin: "git", Kind: ActionCommit, Message: "selected only", Entries: []Entry{selected}, Fingerprints: fingerprints})
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range op.Commands {
		cmd := exec.Command(command.Name, command.Args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v: %s", command.Args, err, out)
		}
	}
	cmd := exec.Command("git", "diff", "--cached", "--name-only")
	cmd.Dir = repo
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(out)) != "unrelated.txt" {
		t.Fatalf("cached paths=%q", out)
	}
}

func TestProposeDeleteUntrackedRequiresSecondConfirmation(t *testing.T) {
	entry := Entry{Path: "tmp dir", Worktree: StateUntracked, DisplayFingerprint: sha256.Sum256([]byte("untracked"))}
	_, err := ProposeAction(ActionRequest{Repo: "/repo", GitBin: "git", Kind: ActionDeleteUntracked, DeleteConfirmed: false, Entries: []Entry{entry}, Fingerprints: []ActionFingerprint{{Path: entry.Path, Status: entry.DisplayFingerprint, Kind: FingerprintDirectory}}})
	if !errors.Is(err, ErrDeleteConfirmationRequired) {
		t.Fatalf("err=%v", err)
	}
}

func TestProposeStashUsesUntrackedFlagOnlyWhenSelected(t *testing.T) {
	tracked := Entry{Path: "tracked", Worktree: StateModified, DisplayFingerprint: sha256.Sum256([]byte("tracked"))}
	untracked := Entry{Path: "new", Worktree: StateUntracked, DisplayFingerprint: sha256.Sum256([]byte("new"))}
	fingerprints := []ActionFingerprint{{Path: tracked.Path, Status: tracked.DisplayFingerprint}, {Path: untracked.Path, Status: untracked.DisplayFingerprint}}
	op, err := ProposeAction(ActionRequest{Repo: "/repo", GitBin: "git", Kind: ActionStash, Entries: []Entry{tracked, untracked}, Fingerprints: fingerprints})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"stash", "push", "-u", "-m", "sys-bozo reviewed stash", "--", "new", "tracked"}
	if !reflect.DeepEqual(op.Commands[0].Args, want) {
		t.Fatalf("args=%#v", op.Commands[0].Args)
	}
}

func TestProposeRestoreRejectsUntrackedAndConflictedSelections(t *testing.T) {
	for _, entry := range []Entry{
		{Path: "new", Worktree: StateUntracked},
		{Path: "conflict", Kind: 'u', Index: StateUnmerged, Worktree: StateUnmerged},
	} {
		entry.DisplayFingerprint = sha256.Sum256([]byte(entry.Path))
		_, err := ProposeAction(ActionRequest{
			Repo: "/repo", GitBin: "git", Kind: ActionRestore,
			Entries: []Entry{entry}, Fingerprints: []ActionFingerprint{{Path: entry.Path, Status: entry.DisplayFingerprint}},
		})
		if !errors.Is(err, ErrInvalidSelection) {
			t.Fatalf("entry=%#v err=%v", entry, err)
		}
	}
}

func TestProposeRenameCoversOriginalAndCurrentPaths(t *testing.T) {
	entry := Entry{Path: "new name", OriginalPath: "old name", Index: StateRenamed, DisplayFingerprint: sha256.Sum256([]byte("rename"))}
	op, err := ProposeAction(ActionRequest{
		Repo: "/repo", GitBin: "git", Kind: ActionCommit, Message: "rename",
		Entries: []Entry{entry}, Fingerprints: []ActionFingerprint{{Path: entry.Path, OriginalPath: entry.OriginalPath, Status: entry.DisplayFingerprint}},
	})
	if err != nil {
		t.Fatal(err)
	}
	wantTail := []string{"--", "new name", "old name"}
	if got := op.Commands[0].Args[len(op.Commands[0].Args)-3:]; !reflect.DeepEqual(got, wantTail) {
		t.Fatalf("args=%#v", op.Commands[0].Args)
	}
}

func TestDeleteUntrackedSymlinkDoesNotFollowTarget(t *testing.T) {
	repo := initTempRepo(t)
	outside := t.TempDir()
	target := filepath.Join(outside, "keep.txt")
	if err := os.WriteFile(target, []byte("keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(repo, "outside-link")); err != nil {
		t.Fatal(err)
	}
	entry := mustInspect(t, repo).Entries[0]
	fingerprints, err := FingerprintEntries(context.Background(), ExecRunner{}, RealFileSystem{}, repo, "git", []Entry{entry})
	if err != nil {
		t.Fatal(err)
	}
	if fingerprints[0].Kind != FingerprintSymlink {
		t.Fatalf("fingerprint=%#v", fingerprints[0])
	}
	op, err := ProposeAction(ActionRequest{
		Repo: repo, GitBin: "git", Kind: ActionDeleteUntracked, DeleteConfirmed: true,
		Entries: []Entry{entry}, Fingerprints: fingerprints,
	})
	if err != nil {
		t.Fatal(err)
	}
	command := op.Commands[0]
	cmd := exec.Command(command.Name, command.Args...)
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clean: %v: %s", err, out)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("symlink target was touched: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(repo, "outside-link")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("link still present or unexpected error: %v", err)
	}
}

func TestRepositoryActionsStayPathScopedInRealTempRepos(t *testing.T) {
	t.Run("stash", func(t *testing.T) {
		repo := initTwoFileRepo(t)
		writeFixture(t, repo, "tracked.txt", "selected change\n")
		writeFixture(t, repo, "other.txt", "other change\n")
		executeSelectedAction(t, repo, ActionStash, "tracked.txt")
		if got := string(gitFixtureOutput(t, repo, "status", "--porcelain")); !strings.Contains(got, "other.txt") || strings.Contains(got, "tracked.txt") {
			t.Fatalf("status=%q", got)
		}
	})

	t.Run("restore", func(t *testing.T) {
		repo := initTwoFileRepo(t)
		writeFixture(t, repo, "tracked.txt", "selected change\n")
		writeFixture(t, repo, "other.txt", "other change\n")
		executeSelectedAction(t, repo, ActionRestore, "tracked.txt")
		data, err := os.ReadFile(filepath.Join(repo, "tracked.txt"))
		if err != nil || string(data) != "base\n" {
			t.Fatalf("selected=%q err=%v", data, err)
		}
		if got := string(gitFixtureOutput(t, repo, "status", "--porcelain")); !strings.Contains(got, "other.txt") || strings.Contains(got, "tracked.txt") {
			t.Fatalf("status=%q", got)
		}
	})

	t.Run("delete", func(t *testing.T) {
		repo := initTempRepo(t)
		writeFixture(t, repo, "selected.tmp", "selected\n")
		writeFixture(t, repo, "other.tmp", "other\n")
		executeSelectedAction(t, repo, ActionDeleteUntracked, "selected.tmp")
		if _, err := os.Stat(filepath.Join(repo, "selected.tmp")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("selected delete err=%v", err)
		}
		if _, err := os.Stat(filepath.Join(repo, "other.tmp")); err != nil {
			t.Fatalf("unselected path touched: %v", err)
		}
	})
}

func initTwoFileRepo(t *testing.T) string {
	t.Helper()
	repo := initTempRepo(t)
	writeFixture(t, repo, "other.txt", "base\n")
	gitFixture(t, repo, "add", "--", "other.txt")
	gitFixture(t, repo, "commit", "-qm", "second fixture")
	return repo
}

func writeFixture(t *testing.T, repo, name, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo, name), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func executeSelectedAction(t *testing.T, repo string, kind ActionKind, selectedPath string) {
	t.Helper()
	status := mustInspect(t, repo)
	var selected Entry
	for _, entry := range status.Entries {
		if entry.Path == selectedPath {
			selected = entry
		}
	}
	if selected.Path == "" {
		t.Fatalf("missing selected path %q in %#v", selectedPath, status.Entries)
	}
	fingerprints, err := FingerprintEntries(context.Background(), ExecRunner{}, RealFileSystem{}, repo, "git", []Entry{selected})
	if err != nil {
		t.Fatal(err)
	}
	op, err := ProposeAction(ActionRequest{
		Repo: repo, GitBin: "git", Kind: kind, Entries: []Entry{selected}, Fingerprints: fingerprints,
		DeleteConfirmed: kind == ActionDeleteUntracked,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateOperation(context.Background(), ExecRunner{}, RealFileSystem{}, op); err != nil {
		t.Fatal(err)
	}
	for _, command := range op.Commands {
		cmd := exec.Command(command.Name, command.Args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v: %s", command.Args, err, out)
		}
	}
}

func gitFixtureOutput(t *testing.T, repo string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return out
}
