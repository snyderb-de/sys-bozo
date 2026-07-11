package fileedit

import (
	"bytes"
	"errors"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestApplyPreservesModeAndRejectsStaleBytes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	if err := os.WriteFile(path, []byte("old\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	proposal := ProposeReplacement(path, []byte("old\n"), []byte("new\n"))
	if err := os.WriteFile(path, []byte("raced\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(proposal); !errors.Is(err, ErrStaleFile) {
		t.Fatalf("err=%v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "raced\n" {
		t.Fatalf("target overwritten: %q", got)
	}
}

func TestFileeditDoesNotImportPackages(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range files {
		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imported := range parsed.Imports {
			name, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			if name == "github.com/snyderb-de/sys-bozo/internal/packages" {
				t.Fatalf("%s imports internal/packages", path)
			}
		}
	}
}

func TestPrepareDefersMutationAndAbortRemovesOwnedArtifacts(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	original := []byte("old\n")
	if err := os.WriteFile(path, original, 0o640); err != nil {
		t.Fatal(err)
	}
	prepared, err := Prepare(ProposeReplacement(path, original, []byte("new\n")))
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(original) {
		t.Fatalf("prepare mutated target: %q err=%v", got, err)
	}
	if err := prepared.Abort(); err != nil {
		t.Fatal(err)
	}
	assertNoApplyTemps(t, dir)
}

func TestPreparedEditCommitAppliesReviewedBytes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	original := []byte("old\n")
	if err := os.WriteFile(path, original, 0o640); err != nil {
		t.Fatal(err)
	}
	prepared, err := Prepare(ProposeReplacement(path, original, []byte("new\n")))
	if err != nil {
		t.Fatal(err)
	}
	applied, err := prepared.Commit()
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "new\n" || string(applied.Before) != string(original) || string(applied.After) != "new\n" {
		t.Fatalf("target=%q applied=%#v err=%v", got, applied, err)
	}
	assertNoApplyTemps(t, dir)
}

func TestPrepareRejectsMutatedProposedBytesBeforeStaging(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	original := []byte("old\n")
	if err := os.WriteFile(path, original, 0o640); err != nil {
		t.Fatal(err)
	}
	proposal := ProposeReplacement(path, original, []byte("new\n"))
	proposal.Proposed[0] = '!'
	prepared, err := Prepare(proposal)
	if prepared != nil {
		_ = prepared.Abort()
	}
	if !errors.Is(err, ErrStaleFile) {
		t.Fatalf("err=%v", err)
	}
	assertNoApplyTemps(t, dir)
}

func TestPreparedEditCommitRejectsTargetReplacementAfterPrepare(t *testing.T) {
	for _, competitor := range [][]byte{[]byte("old\n"), []byte("competitor\n")} {
		t.Run(string(competitor), func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config")
			original := []byte("old\n")
			if err := os.WriteFile(path, original, 0o640); err != nil {
				t.Fatal(err)
			}
			prepared, err := Prepare(ProposeReplacement(path, original, []byte("new\n")))
			if err != nil {
				t.Fatal(err)
			}
			competitorPath := filepath.Join(dir, "competitor")
			if err := os.WriteFile(competitorPath, competitor, 0o640); err != nil {
				t.Fatal(err)
			}
			competitorInfo, err := os.Lstat(competitorPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(competitorPath, path); err != nil {
				t.Fatal(err)
			}

			if _, err := prepared.Commit(); !errors.Is(err, ErrStaleFile) {
				t.Fatalf("err=%v", err)
			}
			got, err := os.ReadFile(path)
			if err != nil || string(got) != string(competitor) {
				t.Fatalf("target=%q err=%v", got, err)
			}
			targetInfo, err := os.Lstat(path)
			if err != nil || !os.SameFile(targetInfo, competitorInfo) {
				t.Fatalf("competitor inode replaced: target=%v competitor=%v err=%v", targetInfo, competitorInfo, err)
			}
			assertNoApplyTemps(t, dir)
		})
	}
}

func TestPreparedEditCommitRevalidatesOriginalModeAndHash(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(string) error
		check  func(*testing.T, string)
	}{
		{
			name:   "hash",
			mutate: func(path string) error { return os.WriteFile(path, []byte("raced\n"), 0o640) },
			check: func(t *testing.T, path string) {
				got, err := os.ReadFile(path)
				if err != nil || string(got) != "raced\n" {
					t.Fatalf("target=%q err=%v", got, err)
				}
			},
		},
		{
			name:   "mode",
			mutate: func(path string) error { return os.Chmod(path, 0o600) },
			check: func(t *testing.T, path string) {
				info, err := os.Stat(path)
				if err != nil || info.Mode().Perm() != 0o600 {
					t.Fatalf("mode=%v err=%v", info, err)
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config")
			original := []byte("old\n")
			if err := os.WriteFile(path, original, 0o640); err != nil {
				t.Fatal(err)
			}
			prepared, err := Prepare(ProposeReplacement(path, original, []byte("new\n")))
			if err != nil {
				t.Fatal(err)
			}
			if err := tc.mutate(path); err != nil {
				t.Fatal(err)
			}
			if _, err := prepared.Commit(); !errors.Is(err, ErrStaleFile) {
				t.Fatalf("err=%v", err)
			}
			tc.check(t, path)
			assertNoApplyTemps(t, dir)
		})
	}
}

func TestPreparedEditCommitReportsRetainedPathsOnCleanupFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	original := []byte("old\n")
	if err := os.WriteFile(path, original, 0o640); err != nil {
		t.Fatal(err)
	}
	cleanupFailure := errors.New("cleanup failed")
	fs := realApplyFilesystem()
	fs.remove = func(string) error { return cleanupFailure }
	fs.hook = func(stage applyStage, target, _ string) {
		if stage != applyBeforeInitialExchange {
			return
		}
		competitor := filepath.Join(dir, "competitor")
		_ = os.WriteFile(competitor, []byte("competitor\n"), 0o640)
		_ = os.Rename(competitor, target)
	}
	prepared, err := prepare(ProposeReplacement(path, original, []byte("new\n")), fs)
	if err != nil {
		t.Fatal(err)
	}
	oldTemp, recovery := prepared.state.oldTemp, prepared.state.recovery
	_, err = prepared.Commit()
	if !errors.Is(err, ErrStaleFile) || !errors.Is(err, cleanupFailure) || !strings.Contains(err.Error(), oldTemp) || !strings.Contains(err.Error(), recovery) {
		t.Fatalf("err=%v oldTemp=%s recovery=%s", err, oldTemp, recovery)
	}
	got, readErr := os.ReadFile(path)
	if readErr != nil || string(got) != "competitor\n" {
		t.Fatalf("target=%q err=%v", got, readErr)
	}
}

func TestAbortRetriesFailedCleanup(t *testing.T) {
	for _, failurePoint := range []string{"remove", "sync"} {
		t.Run(failurePoint, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config")
			original := []byte("old\n")
			if err := os.WriteFile(path, original, 0o640); err != nil {
				t.Fatal(err)
			}
			fs := realApplyFilesystem()
			failure := errors.New("first cleanup failure")
			remove := fs.remove
			syncDir := fs.syncDir
			failed := false
			syncCalls := 0
			fs.remove = func(path string) error {
				if failurePoint == "remove" && !failed {
					failed = true
					return failure
				}
				return remove(path)
			}
			fs.syncDir = func(path string) error {
				syncCalls++
				if failurePoint == "sync" && !failed {
					failed = true
					return failure
				}
				return syncDir(path)
			}
			prepared, err := prepare(ProposeReplacement(path, original, []byte("new\n")), fs)
			if err != nil {
				t.Fatal(err)
			}
			firstErr := prepared.Abort()
			if !errors.Is(firstErr, failure) || (failurePoint == "remove" && !strings.Contains(firstErr.Error(), "artifact")) {
				t.Fatalf("first abort err=%v", firstErr)
			}
			if err := prepared.Abort(); err != nil {
				t.Fatalf("retry abort err=%v", err)
			}
			if failurePoint == "sync" && syncCalls < 2 {
				t.Fatalf("sync calls=%d, cleanup was not retried", syncCalls)
			}
			if err := prepared.Abort(); err != nil {
				t.Fatalf("idempotent abort err=%v", err)
			}
			assertNoApplyTemps(t, dir)
		})
	}
}

func TestAbortRetainsIdentityMismatchedArtifact(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	original := []byte("old\n")
	if err := os.WriteFile(path, original, 0o640); err != nil {
		t.Fatal(err)
	}
	prepared, err := prepare(ProposeReplacement(path, original, []byte("new\n")), realApplyFilesystem())
	if err != nil {
		t.Fatal(err)
	}
	artifact := prepared.state.oldTemp
	replacement := filepath.Join(dir, "replacement")
	if err := os.WriteFile(replacement, []byte("competitor\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, artifact); err != nil {
		t.Fatal(err)
	}
	for attempt := 1; attempt <= 2; attempt++ {
		err := prepared.Abort()
		if err == nil || !strings.Contains(err.Error(), artifact) {
			t.Fatalf("attempt %d err=%v", attempt, err)
		}
		got, readErr := os.ReadFile(artifact)
		if readErr != nil || string(got) != "competitor\n" {
			t.Fatalf("attempt %d artifact=%q err=%v", attempt, got, readErr)
		}
	}
}

func TestCommitRestoresCompetitorRenamedBetweenMetadataAndBytes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	original := []byte("old\n")
	if err := os.WriteFile(path, original, 0o640); err != nil {
		t.Fatal(err)
	}
	competitorPath := filepath.Join(dir, "competitor")
	if err := os.WriteFile(competitorPath, original, 0o640); err != nil {
		t.Fatal(err)
	}
	competitorInfo, err := os.Lstat(competitorPath)
	if err != nil {
		t.Fatal(err)
	}
	fs := realApplyFilesystem()
	stat := fs.stat
	race, replaced := false, false
	fs.stat = func(candidate string) (os.FileInfo, error) {
		info, err := stat(candidate)
		if race && !replaced && candidate == path && err == nil {
			replaced = true
			if renameErr := os.Rename(competitorPath, path); renameErr != nil {
				t.Fatalf("replace target: %v", renameErr)
			}
		}
		return info, err
	}
	prepared, err := prepare(ProposeReplacement(path, original, []byte("new\n")), fs)
	if err != nil {
		t.Fatal(err)
	}
	race = true
	if _, err := prepared.Commit(); !errors.Is(err, ErrStaleFile) {
		t.Fatalf("err=%v", err)
	}
	assertExactTargetIdentity(t, path, competitorInfo, original)
}

func TestCommitRestoresCompetitorAtFinalInterleavings(t *testing.T) {
	for _, stage := range []applyStage{applyAfterCapturedValidation, applyAfterFinalLiveIdentityCheck} {
		t.Run(strconv.Itoa(int(stage)), func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config")
			original := []byte("old\n")
			if err := os.WriteFile(path, original, 0o640); err != nil {
				t.Fatal(err)
			}
			competitorPath := filepath.Join(dir, "competitor")
			if err := os.WriteFile(competitorPath, original, 0o640); err != nil {
				t.Fatal(err)
			}
			competitorInfo, err := os.Lstat(competitorPath)
			if err != nil {
				t.Fatal(err)
			}
			fs := realApplyFilesystem()
			fs.hook = func(got applyStage, target, _ string) {
				if got == stage {
					if err := os.Rename(competitorPath, target); err != nil {
						t.Fatalf("replace target: %v", err)
					}
				}
			}
			_, err = apply(ProposeReplacement(path, original, []byte("new\n")), fs)
			if !errors.Is(err, ErrStaleFile) {
				t.Fatalf("err=%v", err)
			}
			assertExactTargetIdentity(t, path, competitorInfo, original)
		})
	}
}

func TestCommitReportsArtifactsWhenCompetitorRestorationUnverified(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	original := []byte("old\n")
	if err := os.WriteFile(path, original, 0o640); err != nil {
		t.Fatal(err)
	}
	competitorPath := filepath.Join(dir, "competitor")
	if err := os.WriteFile(competitorPath, original, 0o640); err != nil {
		t.Fatal(err)
	}
	restoreFailure := errors.New("restore failed")
	fs := realApplyFilesystem()
	exchange := fs.exchange
	exchanges := 0
	fs.exchange = func(a, b string) error {
		exchanges++
		if exchanges == 2 {
			return restoreFailure
		}
		return exchange(a, b)
	}
	fs.hook = func(stage applyStage, target, _ string) {
		if stage == applyAfterFinalLiveIdentityCheck {
			if err := os.Rename(competitorPath, target); err != nil {
				t.Fatalf("replace target: %v", err)
			}
		}
	}
	prepared, err := prepare(ProposeReplacement(path, original, []byte("new\n")), fs)
	if err != nil {
		t.Fatal(err)
	}
	state := prepared.state
	_, err = prepared.Commit()
	if !errors.Is(err, ErrStaleFile) || !errors.Is(err, restoreFailure) || !strings.Contains(err.Error(), state.oldTemp) || !strings.Contains(err.Error(), state.recovery) || !strings.Contains(err.Error(), state.claim) {
		t.Fatalf("err=%v oldTemp=%s recovery=%s claim=%s", err, state.oldTemp, state.recovery, state.claim)
	}
	got, readErr := os.ReadFile(state.oldTemp)
	if readErr != nil || string(got) != string(original) {
		t.Fatalf("retained competitor=%q err=%v", got, readErr)
	}
}

func assertExactTargetIdentity(t *testing.T, path string, wantInfo os.FileInfo, wantBytes []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, wantBytes) {
		t.Fatalf("target=%q err=%v", got, err)
	}
	gotInfo, err := os.Lstat(path)
	if err != nil || !os.SameFile(gotInfo, wantInfo) {
		t.Fatalf("target inode=%v want=%v err=%v", gotInfo, wantInfo, err)
	}
	if bytes.Equal(got, []byte("new\n")) {
		t.Fatalf("proposal installed at target: %q", got)
	}
}

func TestCommitRejectsFIFOClaimBeforeOpen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	original := []byte("old\n")
	if err := os.WriteFile(path, original, 0o640); err != nil {
		t.Fatal(err)
	}
	prepared, err := Prepare(ProposeReplacement(path, original, []byte("new\n")))
	if err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(dir, "competitor-fifo")
	if err := unix.Mkfifo(fifo, 0o640); err != nil {
		t.Fatal(err)
	}
	fifoInfo, err := os.Lstat(fifo)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(fifo, path); err != nil {
		t.Fatal(err)
	}
	if _, err := commitWithin(t, prepared, 500*time.Millisecond); !errors.Is(err, ErrStaleFile) {
		t.Fatalf("err=%v", err)
	}
	gotInfo, err := os.Lstat(path)
	if err != nil || gotInfo.Mode()&os.ModeNamedPipe == 0 || !os.SameFile(gotInfo, fifoInfo) {
		t.Fatalf("target=%v fifo=%v err=%v", gotInfo, fifoInfo, err)
	}
	assertNoApplyTemps(t, dir)
}

func TestCommitRejectsSymlinkClaimWithoutOpeningReferent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	original := []byte("old\n")
	if err := os.WriteFile(path, original, 0o640); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(dir, "secret-fixture")
	secretBytes := []byte("secret-like-token=do-not-read\n")
	if err := os.WriteFile(secret, secretBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	fs := realApplyFilesystem()
	open := fs.open
	opened := false
	fs.open = func(path string) (applyOpenedFile, error) {
		opened = true
		return open(path)
	}
	prepared, err := prepare(ProposeReplacement(path, original, []byte("new\n")), fs)
	if err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(dir, "competitor-symlink")
	if err := os.Symlink(secret, symlink); err != nil {
		t.Fatal(err)
	}
	symlinkInfo, err := os.Lstat(symlink)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(symlink, path); err != nil {
		t.Fatal(err)
	}
	_, commitErr := commitWithin(t, prepared, 500*time.Millisecond)
	if !errors.Is(commitErr, ErrStaleFile) {
		t.Fatalf("err=%v", commitErr)
	}
	if opened {
		t.Fatal("claim referent was opened")
	}
	if strings.Contains(commitErr.Error(), string(secretBytes)) {
		t.Fatal("claim error exposed referent content")
	}
	gotInfo, err := os.Lstat(path)
	if err != nil || gotInfo.Mode()&os.ModeSymlink == 0 || !os.SameFile(gotInfo, symlinkInfo) {
		t.Fatalf("target=%v symlink=%v err=%v", gotInfo, symlinkInfo, err)
	}
	gotTarget, err := os.Readlink(path)
	if err != nil || gotTarget != secret {
		t.Fatalf("readlink=%q err=%v", gotTarget, err)
	}
	gotSecret, err := os.ReadFile(secret)
	if err != nil || !bytes.Equal(gotSecret, secretBytes) {
		t.Fatalf("secret changed: %q err=%v", gotSecret, err)
	}
	assertNoApplyTemps(t, dir)
}

func commitWithin(t *testing.T, prepared *PreparedEdit, timeout time.Duration) (AppliedEdit, error) {
	t.Helper()
	type result struct {
		applied AppliedEdit
		err     error
	}
	done := make(chan result, 1)
	go func() {
		applied, err := prepared.Commit()
		done <- result{applied: applied, err: err}
	}()
	select {
	case got := <-done:
		return got.applied, got.err
	case <-time.After(timeout):
		t.Fatalf("Commit did not return within %s", timeout)
		return AppliedEdit{}, nil
	}
}

func TestCommitNamesClaimOnEveryRetainedArtifactPath(t *testing.T) {
	t.Run("initial verification", func(t *testing.T) {
		dir, path, original := preparedFixture(t)
		fs := realApplyFilesystem()
		exchange := fs.exchange
		fs.exchange = func(a, b string) error {
			if err := exchange(a, b); err != nil {
				return err
			}
			competitor := filepath.Join(dir, "post-exchange-competitor")
			if err := os.WriteFile(competitor, original, 0o640); err != nil {
				t.Fatal(err)
			}
			return os.Rename(competitor, path)
		}
		prepared, err := prepare(ProposeReplacement(path, original, []byte("new\n")), fs)
		if err != nil {
			t.Fatal(err)
		}
		state := prepared.state
		_, err = prepared.Commit()
		assertRetainedPathsNamed(t, err, state.oldTemp, state.recovery, state.claim)
	})

	t.Run("rollback", func(t *testing.T) {
		_, path, original := preparedFixture(t)
		rollbackFailure := errors.New("rollback failed")
		fs := realApplyFilesystem()
		exchange := fs.exchange
		exchanges := 0
		fs.exchange = func(a, b string) error {
			exchanges++
			if exchanges > 1 {
				return rollbackFailure
			}
			return exchange(a, b)
		}
		fs.hook = func(stage applyStage, target, _ string) {
			if stage == applyBeforeGuardRecheck {
				_ = os.WriteFile(target, []byte("unreviewed\n"), 0o640)
			}
		}
		prepared, err := prepare(ProposeReplacement(path, original, []byte("new\n")), fs)
		if err != nil {
			t.Fatal(err)
		}
		state := prepared.state
		_, err = prepared.Commit()
		if err == nil || !strings.Contains(err.Error(), rollbackFailure.Error()) {
			t.Fatalf("err=%v", err)
		}
		assertRetainedPathsNamed(t, err, state.oldTemp, state.recovery, state.claim)
	})

	t.Run("committed sync", func(t *testing.T) {
		_, path, original := preparedFixture(t)
		syncFailure := errors.New("commit sync failed")
		fs := realApplyFilesystem()
		syncDir := fs.syncDir
		fs.syncDir = func(dir string) error {
			if dir == filepath.Dir(path) {
				return syncFailure
			}
			return syncDir(dir)
		}
		prepared, err := prepare(ProposeReplacement(path, original, []byte("new\n")), fs)
		if err != nil {
			t.Fatal(err)
		}
		state := prepared.state
		applied, err := prepared.Commit()
		if !errors.Is(err, syncFailure) || applied.Path != path {
			t.Fatalf("applied=%#v err=%v", applied, err)
		}
		assertRetainedPathsNamed(t, err, state.oldTemp, state.recovery, state.claim)
	})

	t.Run("claim cleanup", func(t *testing.T) {
		_, path, original := preparedFixture(t)
		cleanupFailure := errors.New("claim cleanup failed")
		fs := realApplyFilesystem()
		remove := fs.remove
		fs.remove = func(path string) error {
			if strings.Contains(path, "claim") {
				return cleanupFailure
			}
			return remove(path)
		}
		prepared, err := prepare(ProposeReplacement(path, original, []byte("new\n")), fs)
		if err != nil {
			t.Fatal(err)
		}
		state := prepared.state
		_, err = prepared.Commit()
		if !errors.Is(err, cleanupFailure) {
			t.Fatalf("err=%v", err)
		}
		assertRetainedPathsNamed(t, err, state.claim)
	})
}

func preparedFixture(t *testing.T) (string, string, []byte) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	original := []byte("old\n")
	if err := os.WriteFile(path, original, 0o640); err != nil {
		t.Fatal(err)
	}
	return dir, path, original
}

func assertRetainedPathsNamed(t *testing.T, err error, paths ...string) {
	t.Helper()
	if err == nil {
		t.Fatalf("err=%v", err)
	}
	for _, path := range paths {
		if path == "" || !strings.Contains(err.Error(), path) {
			t.Fatalf("err=%v missing retained path %q", err, path)
		}
	}
}
