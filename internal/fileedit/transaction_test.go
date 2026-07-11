package fileedit

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestApplyTransactionPreparesEveryEditBeforeWritingAnyTarget(t *testing.T) {
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	a := writeFixture(t, repo, "catalog/tools.yaml", "tools: {}\n")
	b := writeFixture(t, repo, "catalog/hosts.yaml", "hosts: {}\n")
	proposal := TransactionProposal{Repo: repo, LockPath: filepath.Join(repo, ".git", "sys-bozo.lock"), Edits: []Proposal{
		ProposeReplacement(a, []byte("tools: {}\n"), []byte("tools:\n  yazi: {}\n")),
		ProposeReplacement(b, []byte("stale original\n"), []byte("hosts:\n  box: {}\n")),
	}}
	_, err := ApplyTransaction(proposal)
	if !errors.Is(err, ErrStaleFile) {
		t.Fatalf("err=%v", err)
	}
	got, _ := os.ReadFile(a)
	if string(got) != "tools: {}\n" {
		t.Fatalf("first target changed: %q", got)
	}
}

func writeFixture(t *testing.T, repo, relative, content string) string {
	t.Helper()
	path := filepath.Join(repo, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestApplyTransactionSecondCommitFailureRestoresFirstFile(t *testing.T) {
	repo, lockPath := initRepoFixture(t)
	a := writeFixture(t, repo, "catalog/a.yaml", "a: old\n")
	b := writeFixture(t, repo, "catalog/b.yaml", "b: old\n")
	proposal := TransactionProposal{Repo: repo, LockPath: lockPath, Edits: []Proposal{
		ProposeReplacement(b, []byte("b: old\n"), []byte("b: new\n")),
		ProposeReplacement(a, []byte("a: old\n"), []byte("a: new\n")),
	}}
	commits := 0
	transactionHook = func(stage transactionStage, path string) {
		if stage != transactionBeforeCommit {
			return
		}
		commits++
		if commits == 2 {
			replaceFile(t, path, []byte("b: competitor\n"))
		}
	}
	t.Cleanup(func() { transactionHook = nil })

	result, err := ApplyTransaction(proposal)
	if !errors.Is(err, ErrStaleFile) {
		t.Fatalf("err=%v", err)
	}
	if len(result.Edits) != 0 || len(result.RecoveryPaths) != 0 {
		t.Fatalf("result=%#v", result)
	}
	assertFileContent(t, a, "a: old\n")
	assertFileContent(t, b, "b: competitor\n")
}

func TestApplyTransactionPreservesCompetitorBeforeRollback(t *testing.T) {
	repo, lockPath := initRepoFixture(t)
	a := writeFixture(t, repo, "catalog/a.yaml", "a: old\n")
	b := writeFixture(t, repo, "catalog/b.yaml", "b: old\n")
	proposal := TransactionProposal{Repo: repo, LockPath: lockPath, Edits: []Proposal{
		ProposeReplacement(a, []byte("a: old\n"), []byte("a: new\n")),
		ProposeReplacement(b, []byte("b: old\n"), []byte("b: new\n")),
	}}
	commits := 0
	transactionHook = func(stage transactionStage, path string) {
		switch stage {
		case transactionBeforeCommit:
			commits++
			if commits == 2 {
				replaceFile(t, path, []byte("b: competitor\n"))
			}
		case transactionBeforeRollback:
			replaceFile(t, path, []byte("a: competitor\n"))
		}
	}
	t.Cleanup(func() { transactionHook = nil })

	result, err := ApplyTransaction(proposal)
	realA := canonicalPath(t, a)
	if !errors.Is(err, ErrStaleFile) || len(result.Edits) != 1 || result.Edits[0].Path != realA {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if len(result.RecoveryPaths) != 1 || result.RecoveryPaths[0] != realA || !strings.Contains(err.Error(), realA) {
		t.Fatalf("recovery=%v err=%v", result.RecoveryPaths, err)
	}
	assertFileContent(t, a, "a: competitor\n")
}

func TestApplyTransactionRejectsInvalidTargetsBeforePrepare(t *testing.T) {
	repo, lockPath := initRepoFixture(t)
	inside := writeFixture(t, repo, "catalog/inside", "old\n")
	outsideRepo := t.TempDir()
	outside := writeFixture(t, outsideRepo, "outside", "old\n")
	symlink := filepath.Join(repo, "catalog", "symlink")
	if err := os.Symlink(inside, symlink); err != nil {
		t.Fatal(err)
	}
	valid := ProposeReplacement(inside, []byte("old\n"), []byte("new\n"))
	tests := map[string][]Proposal{
		"duplicate": {valid, valid},
		"symlink":   {valid, ProposeReplacement(symlink, []byte("old\n"), []byte("new\n"))},
		"outside":   {valid, ProposeReplacement(outside, []byte("old\n"), []byte("new\n"))},
	}
	for name, edits := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := ApplyTransaction(TransactionProposal{Repo: repo, LockPath: lockPath, Edits: edits}); err == nil {
				t.Fatal("expected validation error")
			}
			assertFileContent(t, inside, "old\n")
			assertNoApplyTemps(t, filepath.Dir(inside))
		})
	}
}

func TestApplyTransactionLockContentionPerformsNoWrites(t *testing.T) {
	repo, lockPath := initRepoFixture(t)
	target := writeFixture(t, repo, "catalog/a", "old\n")
	fd, err := unix.Open(lockPath, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = unix.Flock(fd, unix.LOCK_UN); _ = unix.Close(fd) })

	_, err = ApplyTransaction(TransactionProposal{Repo: repo, LockPath: lockPath, Edits: []Proposal{
		ProposeReplacement(target, []byte("old\n"), []byte("new\n")),
	}})
	if !errors.Is(err, ErrTransactionBusy) {
		t.Fatalf("err=%v", err)
	}
	assertFileContent(t, target, "old\n")
	assertNoApplyTemps(t, filepath.Dir(target))
}

func TestApplyTransactionRejectsOutsideLockPath(t *testing.T) {
	repo, _ := initRepoFixture(t)
	target := writeFixture(t, repo, "catalog/a", "old\n")
	outsideLock := filepath.Join(t.TempDir(), repoLockName)
	_, err := ApplyTransaction(TransactionProposal{Repo: repo, LockPath: outsideLock, Edits: []Proposal{
		ProposeReplacement(target, []byte("old\n"), []byte("new\n")),
	}})
	if err == nil {
		t.Fatal("outside lock path accepted")
	}
	if _, statErr := os.Lstat(outsideLock); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("outside lock was created: %v", statErr)
	}
	assertFileContent(t, target, "old\n")
}

func TestResolveRepoLockPathUsesLinkedWorktreeCommonDirectory(t *testing.T) {
	root := t.TempDir()
	mainRepo := filepath.Join(root, "main")
	worktree := filepath.Join(root, "worktree")
	common := filepath.Join(mainRepo, ".git")
	gitDir := filepath.Join(common, "worktrees", "worktree")
	for _, path := range []string{common, gitDir, worktree} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: "+gitDir+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "commondir"), []byte("../..\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := ResolveRepoLockPath(worktree)
	if err != nil || got != canonicalPath(t, filepath.Join(common, repoLockName)) {
		t.Fatalf("lock=%q err=%v", got, err)
	}
	target := writeFixture(t, worktree, "catalog/a", "old\n")
	wrong := filepath.Join(gitDir, repoLockName)
	_, err = ApplyTransaction(TransactionProposal{Repo: worktree, LockPath: wrong, Edits: []Proposal{
		ProposeReplacement(target, []byte("old\n"), []byte("new\n")),
	}})
	if err == nil {
		t.Fatal("per-worktree lock was accepted")
	}
}

func TestResolveRepoLockPathRejectsSymlinkMetadata(t *testing.T) {
	repo := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(repo, ".git")); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveRepoLockPath(repo); err == nil {
		t.Fatal("symlink .git accepted")
	}
}

func TestResolveRepoLockPathRejectsSymlinkCommonDirectory(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "worktree")
	gitDir := filepath.Join(root, "gitdir")
	common := filepath.Join(root, "common")
	commonLink := filepath.Join(root, "common-link")
	for _, path := range []string{repo, gitDir, common} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(common, commonLink); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".git"), []byte("gitdir: "+gitDir+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "commondir"), []byte(commonLink+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveRepoLockPath(repo); err == nil {
		t.Fatal("symlink common directory accepted")
	}
}

func TestApplyTransactionReportsEveryUnresolvedRecoveryPath(t *testing.T) {
	repo, lockPath := initRepoFixture(t)
	paths := []string{
		writeFixture(t, repo, "catalog/a", "a-old\n"),
		writeFixture(t, repo, "catalog/b", "b-old\n"),
		writeFixture(t, repo, "catalog/c", "c-old\n"),
	}
	edits := make([]Proposal, len(paths))
	for i, path := range paths {
		edits[i] = ProposeReplacement(path, []byte(string(rune('a'+i))+"-old\n"), []byte(string(rune('a'+i))+"-new\n"))
	}
	commits := 0
	var rollbackOrder []string
	transactionHook = func(stage transactionStage, path string) {
		switch stage {
		case transactionBeforeCommit:
			commits++
			if commits == 3 {
				replaceFile(t, path, []byte("c-competitor\n"))
			}
		case transactionBeforeRollback:
			rollbackOrder = append(rollbackOrder, path)
			replaceFile(t, path, []byte(filepath.Base(path)+"-competitor\n"))
		}
	}
	t.Cleanup(func() { transactionHook = nil })

	result, err := ApplyTransaction(TransactionProposal{Repo: repo, LockPath: lockPath, Edits: edits})
	if !errors.Is(err, ErrStaleFile) || len(result.Edits) != 2 || len(result.RecoveryPaths) != 2 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	for _, path := range paths[:2] {
		realPath := canonicalPath(t, path)
		if !contains(result.RecoveryPaths, realPath) || !strings.Contains(err.Error(), realPath) {
			t.Fatalf("missing path %s: result=%#v err=%v", path, result, err)
		}
	}
	if want := []string{canonicalPath(t, paths[1]), canonicalPath(t, paths[0])}; !slicesEqual(rollbackOrder, want) {
		t.Fatalf("rollback order=%v want=%v", rollbackOrder, want)
	}
}

func TestApplyTransactionLinkedWorktreesContendOnCommonLock(t *testing.T) {
	root := t.TempDir()
	common := filepath.Join(root, "main", ".git")
	if err := os.MkdirAll(filepath.Join(common, "worktrees"), 0o700); err != nil {
		t.Fatal(err)
	}
	makeWorktree := func(name string) (string, string) {
		repo := filepath.Join(root, name)
		gitDir := filepath.Join(common, "worktrees", name)
		if err := os.MkdirAll(gitDir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(repo, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo, ".git"), []byte("gitdir: "+gitDir+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(gitDir, "commondir"), []byte("../..\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return repo, writeFixture(t, repo, "catalog/a", "old\n")
	}
	repoA, _ := makeWorktree("one")
	repoB, targetB := makeWorktree("two")
	lockA, err := ResolveRepoLockPath(repoA)
	if err != nil {
		t.Fatal(err)
	}
	lockB, err := ResolveRepoLockPath(repoB)
	if err != nil || lockA != lockB {
		t.Fatalf("lockA=%q lockB=%q err=%v", lockA, lockB, err)
	}
	release, err := acquireRepoLock(lockA)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = release() })

	_, err = ApplyTransaction(TransactionProposal{Repo: repoB, LockPath: lockB, Edits: []Proposal{
		ProposeReplacement(targetB, []byte("old\n"), []byte("new\n")),
	}})
	if !errors.Is(err, ErrTransactionBusy) {
		t.Fatalf("err=%v", err)
	}
	assertFileContent(t, targetB, "old\n")
}

func TestProposeTransactionRevertRejectsAnyChangedTarget(t *testing.T) {
	repo, lockPath := initRepoFixture(t)
	a := writeFixture(t, repo, "catalog/a", "a-old\n")
	b := writeFixture(t, repo, "catalog/b", "b-old\n")
	result, err := ApplyTransaction(TransactionProposal{Repo: repo, LockPath: lockPath, Edits: []Proposal{
		ProposeReplacement(a, []byte("a-old\n"), []byte("a-new\n")),
		ProposeReplacement(b, []byte("b-old\n"), []byte("b-new\n")),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte("b-changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ProposeTransactionRevert(result, repo, lockPath); !errors.Is(err, ErrStaleFile) {
		t.Fatalf("err=%v", err)
	}
}

func TestTransactionSuccessAndRevertMetadataOwnTheirBytes(t *testing.T) {
	repo, lockPath := initRepoFixture(t)
	target := writeFixture(t, repo, "catalog/a", "old\n")
	original := []byte("old\n")
	proposed := []byte("new\n")
	result, err := ApplyTransaction(TransactionProposal{Repo: repo, LockPath: lockPath, Edits: []Proposal{
		ProposeReplacement(target, original, proposed),
	}})
	if err != nil || len(result.Edits) != 1 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	original[0], proposed[0] = 'X', 'Y'
	if string(result.Edits[0].Before) != "old\n" || string(result.Edits[0].After) != "new\n" {
		t.Fatalf("result aliases caller bytes: %#v", result.Edits[0])
	}
	revert, err := ProposeTransactionRevert(result, repo, lockPath)
	if err != nil || len(revert.Edits) != 1 {
		t.Fatalf("revert=%#v err=%v", revert, err)
	}
	result.Edits[0].Before[0] = '!'
	if string(revert.Edits[0].Proposed) != "old\n" {
		t.Fatalf("revert aliases result bytes: %q", revert.Edits[0].Proposed)
	}
	reverted, err := ApplyTransaction(revert)
	if err != nil || len(reverted.Edits) != 1 {
		t.Fatalf("reverted=%#v err=%v", reverted, err)
	}
	assertFileContent(t, target, "old\n")
}

func initRepoFixture(t *testing.T) (string, string) {
	t.Helper()
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	return repo, filepath.Join(repo, ".git", repoLockName)
}

func replaceFile(t *testing.T, path string, content []byte) {
	t.Helper()
	replacement := path + ".competitor"
	if err := os.WriteFile(replacement, content, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, []byte(want)) {
		t.Fatalf("path=%s content=%q want=%q err=%v", path, got, want, err)
	}
}

func contains(paths []string, want string) bool {
	for _, path := range paths {
		if path == want {
			return true
		}
	}
	return false
}

func slicesEqual(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func canonicalPath(t *testing.T, path string) string {
	t.Helper()
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		parent, parentErr := filepath.EvalSymlinks(filepath.Dir(path))
		if parentErr != nil {
			t.Fatal(parentErr)
		}
		return filepath.Join(parent, filepath.Base(path))
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return real
}
