package fileedit

import (
	"errors"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
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
