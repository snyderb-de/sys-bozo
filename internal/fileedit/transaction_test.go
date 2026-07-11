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

func TestApplyTransactionReportsCurrentEditWhenPostExchangeRestoreIsUnverified(t *testing.T) {
	repo, lockPath := initRepoFixture(t)
	target := writeFixture(t, repo, "catalog/a.yaml", "a: old\n")
	restoreFailure := errors.New("injected post-exchange restore failure")
	transactionPrepare = func(proposal Proposal) (*PreparedEdit, error) {
		fs := realApplyFilesystem()
		exchanges := 0
		originalExchange := fs.exchange
		fs.exchange = func(a, b string) error {
			exchanges++
			if exchanges > 1 {
				return restoreFailure
			}
			return originalExchange(a, b)
		}
		fs.hook = func(stage applyStage, _, displaced string) {
			if stage == applyBeforeGuardRecheck {
				_ = os.WriteFile(displaced, []byte("tampered old inode\n"), 0o600)
			}
		}
		return prepare(proposal, fs)
	}
	t.Cleanup(func() { transactionPrepare = Prepare })

	result, err := ApplyTransaction(TransactionProposal{Repo: repo, LockPath: lockPath, Edits: []Proposal{
		ProposeReplacement(target, []byte("a: old\n"), []byte("a: new\n")),
	}})
	realTarget := canonicalPath(t, target)
	if !strings.Contains(err.Error(), restoreFailure.Error()) || len(result.Edits) != 1 || result.Edits[0].Path != realTarget {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if len(result.RecoveryPaths) != 1 || result.RecoveryPaths[0] != realTarget {
		t.Fatalf("recovery=%v", result.RecoveryPaths)
	}
}

func TestPreparedEditOutcomeDistinguishesCommitStates(t *testing.T) {
	t.Run("verified applied", func(t *testing.T) {
		dir := t.TempDir()
		path := writeFixture(t, dir, "target", "old\n")
		prepared, err := Prepare(ProposeReplacement(path, []byte("old\n"), []byte("new\n")))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := prepared.Commit(); err != nil {
			t.Fatal(err)
		}
		outcome, state := prepared.Outcome()
		if state != CommitVerifiedApplied || outcome.Path != path {
			t.Fatalf("state=%v outcome=%#v", state, outcome)
		}
	})

	t.Run("not applied before exchange", func(t *testing.T) {
		dir := t.TempDir()
		path := writeFixture(t, dir, "target", "old\n")
		prepared, err := Prepare(ProposeReplacement(path, []byte("old\n"), []byte("new\n")))
		if err != nil {
			t.Fatal(err)
		}
		replaceFile(t, path, []byte("competitor\n"))
		if _, err := prepared.Commit(); !errors.Is(err, ErrStaleFile) {
			t.Fatalf("err=%v", err)
		}
		outcome, state := prepared.Outcome()
		if state != CommitNotApplied || outcome.Path != "" {
			t.Fatalf("state=%v outcome=%#v", state, outcome)
		}
	})

	t.Run("verified restored after exchange", func(t *testing.T) {
		dir := t.TempDir()
		path := writeFixture(t, dir, "target", "old\n")
		fs := realApplyFilesystem()
		fs.hook = func(stage applyStage, _, displaced string) {
			if stage == applyBeforeGuardRecheck {
				_ = os.WriteFile(displaced, []byte("tampered\n"), 0o600)
			}
		}
		prepared, err := prepare(ProposeReplacement(path, []byte("old\n"), []byte("new\n")), fs)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := prepared.Commit(); !errors.Is(err, ErrStaleFile) {
			t.Fatalf("err=%v", err)
		}
		outcome, state := prepared.Outcome()
		if state != CommitVerifiedRestored || outcome.Path != path {
			t.Fatalf("state=%v outcome=%#v", state, outcome)
		}
		assertFileContent(t, path, "old\n")
	})
}

func TestApplyTransactionDoesNotReportSimplePreExchangeFailureAsApplied(t *testing.T) {
	repo, lockPath := initRepoFixture(t)
	target := writeFixture(t, repo, "catalog/a", "old\n")
	transactionHook = func(stage transactionStage, path string) {
		if stage == transactionBeforeCommit {
			replaceFile(t, path, []byte("competitor\n"))
		}
	}
	t.Cleanup(func() { transactionHook = nil })
	result, err := ApplyTransaction(TransactionProposal{Repo: repo, LockPath: lockPath, Edits: []Proposal{
		ProposeReplacement(target, []byte("old\n"), []byte("new\n")),
	}})
	if !errors.Is(err, ErrStaleFile) || len(result.Edits) != 0 || len(result.RecoveryPaths) != 0 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
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

func TestApplyTransactionOmitsVerifiedRollbackDespiteCleanupOrSyncError(t *testing.T) {
	for _, failurePoint := range []string{"sync", "cleanup"} {
		t.Run(failurePoint, func(t *testing.T) {
			repo, lockPath := initRepoFixture(t)
			a := writeFixture(t, repo, "catalog/a", "a-old\n")
			b := writeFixture(t, repo, "catalog/b", "b-old\n")
			realA := canonicalPath(t, a)
			rollbackFailure := errors.New("injected verified rollback tail failure")
			transactionPrepare = func(proposal Proposal) (*PreparedEdit, error) {
				if proposal.Path != realA || string(proposal.Original) != "a-new\n" || string(proposal.Proposed) != "a-old\n" {
					return Prepare(proposal)
				}
				fs := realApplyFilesystem()
				failed := false
				switch failurePoint {
				case "sync":
					originalSync := fs.syncDir
					fs.syncDir = func(path string) error {
						if path == filepath.Dir(realA) && !failed {
							failed = true
							return rollbackFailure
						}
						return originalSync(path)
					}
				case "cleanup":
					originalRemove := fs.remove
					fs.remove = func(path string) error {
						if !failed {
							failed = true
							return rollbackFailure
						}
						return originalRemove(path)
					}
				}
				return prepare(proposal, fs)
			}
			t.Cleanup(func() { transactionPrepare = Prepare })
			commits := 0
			transactionHook = func(stage transactionStage, path string) {
				if stage == transactionBeforeCommit {
					commits++
					if commits == 2 {
						replaceFile(t, path, []byte("b-competitor\n"))
					}
				}
			}
			t.Cleanup(func() { transactionHook = nil })

			result, err := ApplyTransaction(TransactionProposal{Repo: repo, LockPath: lockPath, Edits: []Proposal{
				ProposeReplacement(a, []byte("a-old\n"), []byte("a-new\n")),
				ProposeReplacement(b, []byte("b-old\n"), []byte("b-new\n")),
			}})
			if !errors.Is(err, rollbackFailure) {
				t.Fatalf("err=%v", err)
			}
			if len(result.Edits) != 0 || len(result.RecoveryPaths) != 0 {
				t.Fatalf("verified rollback reported as unresolved: %#v", result)
			}
			assertFileContent(t, a, "a-old\n")
		})
	}
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

func TestApplyTransactionDoesNotAcquireLockThroughReplacedCommonDirectory(t *testing.T) {
	repo, lockPath := initRepoFixture(t)
	target := writeFixture(t, repo, "catalog/a", "old\n")
	gitDir := filepath.Join(repo, ".git")
	movedGitDir := filepath.Join(repo, ".git-reviewed")
	outside := t.TempDir()
	transactionHook = func(stage transactionStage, _ string) {
		if stage != transactionBeforeLock {
			return
		}
		if err := os.Rename(gitDir, movedGitDir); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, gitDir); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { transactionHook = nil })

	_, err := ApplyTransaction(TransactionProposal{Repo: repo, LockPath: lockPath, Edits: []Proposal{
		ProposeReplacement(target, []byte("old\n"), []byte("new\n")),
	}})
	if err == nil {
		t.Fatal("replaced common directory accepted")
	}
	if _, statErr := os.Lstat(filepath.Join(outside, repoLockName)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("wrong lock acquired: %v", statErr)
	}
	assertFileContent(t, target, "old\n")
}

func TestApplyTransactionRejectsReplacedParentBeforePrepare(t *testing.T) {
	repo, lockPath := initRepoFixture(t)
	target := writeFixture(t, repo, "catalog/a", "inside\n")
	outside := t.TempDir()
	outsideTarget := writeFixture(t, outside, "a", "outside\n")
	swapDirectoryAtStage(t, transactionBeforePrepare, filepath.Dir(target), outside)

	_, err := ApplyTransaction(TransactionProposal{Repo: repo, LockPath: lockPath, Edits: []Proposal{
		ProposeReplacement(target, []byte("inside\n"), []byte("changed\n")),
	}})
	if !errors.Is(err, ErrStaleFile) {
		t.Fatalf("err=%v", err)
	}
	assertFileContent(t, outsideTarget, "outside\n")
}

func TestApplyTransactionRejectsReplacedParentAfterPrepare(t *testing.T) {
	repo, lockPath := initRepoFixture(t)
	target := writeFixture(t, repo, "catalog/a", "inside\n")
	outside := t.TempDir()
	outsideTarget := writeFixture(t, outside, "a", "outside\n")
	swapDirectoryAtStage(t, transactionAfterPrepareAll, filepath.Dir(target), outside)

	result, err := ApplyTransaction(TransactionProposal{Repo: repo, LockPath: lockPath, Edits: []Proposal{
		ProposeReplacement(target, []byte("inside\n"), []byte("changed\n")),
	}})
	if !errors.Is(err, ErrStaleFile) {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if len(result.Edits) != 0 || len(result.RecoveryPaths) != 1 || result.RecoveryPaths[0] != canonicalMissingPath(t, target) {
		t.Fatalf("result=%#v", result)
	}
	assertFileContent(t, outsideTarget, "outside\n")
}

func TestApplyTransactionRevalidatesParentImmediatelyAfterPrepare(t *testing.T) {
	repo, lockPath := initRepoFixture(t)
	target := writeFixture(t, repo, "catalog/a", "inside\n")
	outside := t.TempDir()
	outsideTarget := writeFixture(t, outside, "a", "outside\n")
	transactionPrepare = func(proposal Proposal) (*PreparedEdit, error) {
		prepared, err := Prepare(proposal)
		if err != nil {
			return nil, err
		}
		directory := filepath.Dir(proposal.Path)
		if err := os.Rename(directory, directory+".reviewed"); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, directory); err != nil {
			t.Fatal(err)
		}
		return prepared, nil
	}
	t.Cleanup(func() { transactionPrepare = Prepare })

	result, err := ApplyTransaction(TransactionProposal{Repo: repo, LockPath: lockPath, Edits: []Proposal{
		ProposeReplacement(target, []byte("inside\n"), []byte("changed\n")),
	}})
	if !errors.Is(err, ErrStaleFile) || len(result.Edits) != 0 || len(result.RecoveryPaths) != 1 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	assertFileContent(t, outsideTarget, "outside\n")
}

func TestApplyTransactionRejectsReplacedParentBeforeRollbackPrepare(t *testing.T) {
	repo, lockPath := initRepoFixture(t)
	a := writeFixture(t, repo, "catalog/a", "a-old\n")
	b := writeFixture(t, repo, "catalog/b", "b-old\n")
	outside := t.TempDir()
	outsideA := writeFixture(t, outside, "a", "outside\n")
	commits := 0
	swapped := false
	transactionHook = func(stage transactionStage, path string) {
		switch stage {
		case transactionBeforeCommit:
			commits++
			if commits == 2 {
				replaceFile(t, path, []byte("b-competitor\n"))
			}
		case transactionBeforeRollback:
			if !swapped {
				swapped = true
				directory := filepath.Dir(path)
				if err := os.Rename(directory, directory+".reviewed"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, directory); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	t.Cleanup(func() { transactionHook = nil })

	result, err := ApplyTransaction(TransactionProposal{Repo: repo, LockPath: lockPath, Edits: []Proposal{
		ProposeReplacement(a, []byte("a-old\n"), []byte("a-new\n")),
		ProposeReplacement(b, []byte("b-old\n"), []byte("b-new\n")),
	}})
	if !errors.Is(err, ErrStaleFile) || len(result.Edits) != 1 || len(result.RecoveryPaths) != 1 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	assertFileContent(t, outsideA, "outside\n")
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
	resolvedLock, err := resolveRepoLock(repoA)
	if err != nil {
		t.Fatal(err)
	}
	release, err := acquireRepoLock(resolvedLock)
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

func swapDirectoryAtStage(t *testing.T, wanted transactionStage, directory, outside string) {
	t.Helper()
	moved := directory + ".reviewed"
	done := false
	transactionHook = func(stage transactionStage, _ string) {
		if stage != wanted || done {
			return
		}
		done = true
		if err := os.Rename(directory, moved); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, directory); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { transactionHook = nil })
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

func canonicalMissingPath(t *testing.T, path string) string {
	t.Helper()
	repoParent, err := filepath.EvalSymlinks(filepath.Dir(filepath.Dir(path)))
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(repoParent, filepath.Base(filepath.Dir(path)), filepath.Base(path))
}
