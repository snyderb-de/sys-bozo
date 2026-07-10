package packages

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyRejectsStaleFileAndPreservesIt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "packages.nix")
	original := []byte("home.packages = [\n  # Misc\n];\n")
	if err := os.WriteFile(path, original, 0o640); err != nil {
		t.Fatal(err)
	}
	proposal, err := ProposeAdd(original, Target{Path: path, Assignment: "home.packages"}, "Misc", "yazi")
	if err != nil {
		t.Fatal(err)
	}
	concurrent := []byte("concurrent edit\n")
	if err := os.WriteFile(path, concurrent, 0o640); err != nil {
		t.Fatal(err)
	}

	_, err = Apply(proposal)
	if !errors.Is(err, ErrStaleFile) {
		t.Fatalf("err=%v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, concurrent) {
		t.Fatalf("file=%q", got)
	}
	assertNoApplyTemps(t, dir)
}

func TestApplyPreservesPermissionsAndReturnsExactEdit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "homebrew.nix")
	original := []byte("casks = [\n  # Misc\n];\n")
	if err := os.WriteFile(path, original, 0o640); err != nil {
		t.Fatal(err)
	}
	proposal, err := ProposeAdd(original, Target{Path: path, Assignment: "casks", Quoted: true}, "Misc", "zed")
	if err != nil {
		t.Fatal(err)
	}

	applied, err := Apply(proposal)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("mode=%o", info.Mode().Perm())
	}
	if applied.Path != path || !bytes.Equal(applied.Before, original) || !bytes.Equal(applied.After, proposal.Proposed) {
		t.Fatalf("applied=%#v", applied)
	}
	if applied.BeforeHash != sha256.Sum256(original) || applied.AfterHash != sha256.Sum256(proposal.Proposed) {
		t.Fatalf("hashes before=%x after=%x", applied.BeforeHash, applied.AfterHash)
	}

	proposal.Proposed[0] = '!'
	if applied.After[0] != 'c' {
		t.Fatal("applied edit retained proposal-owned bytes")
	}
	assertNoApplyTemps(t, dir)
}

func TestProposeRevertChecksPostHashAndRestoresExactBytes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "packages.nix")
	original := []byte("home.packages = [\r\n  # Misc\r\n];")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	proposal, err := ProposeAdd(original, Target{Path: path, Assignment: "home.packages"}, "Misc", "yazi")
	if err != nil {
		t.Fatal(err)
	}
	applied, err := Apply(proposal)
	if err != nil {
		t.Fatal(err)
	}

	concurrent := append(bytes.Clone(applied.After), []byte("\n# drift\n")...)
	if err := os.WriteFile(path, concurrent, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ProposeRevert(applied); !errors.Is(err, ErrStaleFile) {
		t.Fatalf("stale revert err=%v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, concurrent) {
		t.Fatalf("stale revert changed file=%q", got)
	}

	if err := os.WriteFile(path, applied.After, 0o600); err != nil {
		t.Fatal(err)
	}
	revert, err := ProposeRevert(applied)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(revert.Original, applied.After) || !bytes.Equal(revert.Proposed, original) {
		t.Fatalf("revert original=%q proposed=%q", revert.Original, revert.Proposed)
	}
	if revert.OriginalHash != applied.AfterHash || revert.ProposedHash != applied.BeforeHash {
		t.Fatalf("revert hashes original=%x proposed=%x", revert.OriginalHash, revert.ProposedHash)
	}
	if !strings.Contains(revert.Diff, "-  yazi\r\n") {
		t.Fatalf("reverse diff=%q", revert.Diff)
	}
	if _, err := Apply(revert); err != nil {
		t.Fatal(err)
	}
	got, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, original) {
		t.Fatalf("file=%q", got)
	}
	assertNoApplyTemps(t, dir)
}

func TestProposeReplacementOwnsHashesUnifiedDiffAndBytes(t *testing.T) {
	original := []byte("first\nsecond\n")
	proposed := []byte("first\nreplacement\nlast\n")
	target := Target{Path: "/fixture/packages.nix", ApplyAction: "hms"}
	proposal := ProposeReplacement(target, original, proposed)
	if proposal.Target != target || proposal.OriginalHash != sha256.Sum256(original) || proposal.ProposedHash != sha256.Sum256(proposed) {
		t.Fatalf("proposal=%#v", proposal)
	}
	for _, want := range []string{"--- original", "+++ proposed", "@@ -1,2 +1,3 @@", "-second", "+replacement", "+last"} {
		if !strings.Contains(proposal.Diff, want) {
			t.Fatalf("diff missing %q: %q", want, proposal.Diff)
		}
	}
	original[0], proposed[0] = 'X', 'Y'
	if string(proposal.Original) != "first\nsecond\n" || string(proposal.Proposed) != "first\nreplacement\nlast\n" {
		t.Fatalf("proposal aliases caller bytes: original=%q proposed=%q", proposal.Original, proposal.Proposed)
	}
}

func TestApplyCleansTemporaryFileOnEveryPreRenameFailure(t *testing.T) {
	failure := errors.New("injected apply failure")
	stages := []string{"chmod", "write", "sync", "close", "rename"}

	for _, stage := range stages {
		t.Run(stage, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "packages.nix")
			original := []byte("home.packages = [\n  # Misc\n];\n")
			if err := os.WriteFile(path, original, 0o640); err != nil {
				t.Fatal(err)
			}
			proposal, err := ProposeAdd(original, Target{Path: path, Assignment: "home.packages"}, "Misc", "yazi")
			if err != nil {
				t.Fatal(err)
			}
			filesystem := realApplyFilesystem()
			createTemp := filesystem.createTemp
			filesystem.createTemp = func(tempDir, pattern string) (applyTempFile, error) {
				if tempDir != dir {
					t.Fatalf("temp dir=%q want %q", tempDir, dir)
				}
				file, err := createTemp(tempDir, pattern)
				if err != nil {
					return nil, err
				}
				return &failingApplyTempFile{File: file.(*os.File), stage: stage, err: failure}, nil
			}
			if stage == "rename" {
				filesystem.exchange = func(_, _ string) error { return failure }
			}

			_, err = apply(proposal, filesystem)
			if !errors.Is(err, failure) {
				t.Fatalf("err=%v", err)
			}
			got, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if !bytes.Equal(got, original) {
				t.Fatalf("target changed after %s failure: %q", stage, got)
			}
			assertNoApplyTemps(t, dir)
		})
	}
}

func TestApplyRejectsFileChangedWhilePreparingTemporaryFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "packages.nix")
	original := []byte("home.packages = [\n  # Misc\n];\n")
	if err := os.WriteFile(path, original, 0o640); err != nil {
		t.Fatal(err)
	}
	proposal, err := ProposeAdd(original, Target{Path: path, Assignment: "home.packages"}, "Misc", "yazi")
	if err != nil {
		t.Fatal(err)
	}
	concurrent := []byte("concurrent edit during apply\n")
	filesystem := realApplyFilesystem()
	createTemp := filesystem.createTemp
	filesystem.createTemp = func(tempDir, pattern string) (applyTempFile, error) {
		file, err := createTemp(tempDir, pattern)
		if err != nil {
			return nil, err
		}
		return &concurrentEditApplyTempFile{
			applyTempFile: file,
			onClose: func() error {
				return os.WriteFile(path, concurrent, 0o640)
			},
		}, nil
	}

	_, err = apply(proposal, filesystem)
	if !errors.Is(err, ErrStaleFile) {
		t.Fatalf("err=%v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, concurrent) {
		t.Fatalf("file=%q", got)
	}
	assertNoApplyTemps(t, dir)
}

func TestApplyRejectsSymlinkAndNonRegularTargets(t *testing.T) {
	dir := t.TempDir()
	original := []byte("original\n")
	regular := filepath.Join(dir, "regular")
	if err := os.WriteFile(regular, original, 0o600); err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(dir, "symlink")
	if err := os.Symlink(regular, symlink); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{symlink, dir} {
		_, err := Apply(ProposeReplacement(Target{Path: path}, original, []byte("proposed\n")))
		if err == nil || errors.Is(err, ErrStaleFile) {
			t.Fatalf("path=%q err=%v", path, err)
		}
	}
	got, err := os.ReadFile(regular)
	if err != nil || !bytes.Equal(got, original) {
		t.Fatalf("regular=%q err=%v", got, err)
	}
}

func TestApplyRaceHooksNeverOverwriteCompetingEdits(t *testing.T) {
	tests := []struct {
		name  string
		stage applyStage
		hook  func(string, string)
		want  []byte
	}{
		{
			name: "before claim", stage: applyBeforeClaim, want: []byte("before-claim\n"),
			hook: func(target, _ string) { _ = os.WriteFile(target, []byte("before-claim\n"), 0o600) },
		},
		{
			name: "competing target before install", stage: applyBeforeInstall, want: []byte("original\n"),
			hook: func(target, _ string) { _ = os.WriteFile(target, []byte("competitor\n"), 0o600) },
		},
		{
			name: "old inode write after claim", stage: applyBeforeGuardRecheck, want: []byte("original\n"),
			hook: func(_, guard string) { _ = os.WriteFile(guard, []byte("old-inode-write\n"), 0o600) },
		},
		{
			name: "installed inode write", stage: applyBeforeGuardRecheck, want: []byte("original\n"),
			hook: func(target, _ string) { _ = os.WriteFile(target, []byte("unreviewed\n"), 0o600) },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "target")
			original := []byte("original\n")
			if err := os.WriteFile(path, original, 0o600); err != nil {
				t.Fatal(err)
			}
			fs := realApplyFilesystem()
			fs.hook = func(stage applyStage, target, guard string) {
				if stage == tt.stage {
					tt.hook(target, guard)
				}
			}
			_, err := apply(ProposeReplacement(Target{Path: path}, original, []byte("proposed\n")), fs)
			if !errors.Is(err, ErrStaleFile) {
				t.Fatalf("err=%v", err)
			}
			got, readErr := os.ReadFile(path)
			if readErr != nil || !bytes.Equal(got, tt.want) {
				t.Fatalf("target=%q err=%v want=%q", got, readErr, tt.want)
			}
		})
	}
}

func TestApplyAfterClaimHookCanObserveClaimedTarget(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "target")
	original := []byte("original\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	seen := false
	fs := realApplyFilesystem()
	fs.hook = func(stage applyStage, target, guard string) {
		if stage != applyAfterClaim {
			return
		}
		seen = true
		if got, err := os.ReadFile(target); err != nil || !bytes.Equal(got, []byte("proposed\n")) {
			t.Fatalf("target unavailable/unexpected: %q %v", got, err)
		}
		if got, err := os.ReadFile(guard); err != nil || !bytes.Equal(got, original) {
			t.Fatalf("guard=%q err=%v", got, err)
		}
	}
	if _, err := apply(ProposeReplacement(Target{Path: path}, original, []byte("proposed\n")), fs); err != nil {
		t.Fatal(err)
	}
	if !seen {
		t.Fatal("after-claim hook not called")
	}
}

func TestApplyTargetExistsWithOldOrNewBytesAtEveryHook(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "target")
	original := []byte("old\n")
	proposed := []byte("new\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	seen := map[applyStage]bool{}
	fs := realApplyFilesystem()
	fs.hook = func(stage applyStage, target, _ string) {
		seen[stage] = true
		got, err := os.ReadFile(target)
		if err != nil || (!bytes.Equal(got, original) && !bytes.Equal(got, proposed)) {
			t.Fatalf("stage=%v target=%q err=%v", stage, got, err)
		}
	}
	if _, err := apply(ProposeReplacement(Target{Path: path}, original, proposed), fs); err != nil {
		t.Fatal(err)
	}
	for _, stage := range []applyStage{applyBeforeClaim, applyAfterClaim, applyBeforeInstall, applyBeforeGuardRecheck} {
		if !seen[stage] {
			t.Fatalf("stage %v unseen", stage)
		}
	}
}

func TestApplyRollbackPreservesRenameOverCompetingTargetByIdentity(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "target")
	original := []byte("old\n")
	proposed := []byte("new\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	fs := realApplyFilesystem()
	fs.hook = func(stage applyStage, target, _ string) {
		if stage == applyAfterClaim {
			competitor := filepath.Join(dir, "competitor")
			_ = os.WriteFile(competitor, proposed, 0o600)
			_ = os.Rename(competitor, target)
		}
	}
	_, err := apply(ProposeReplacement(Target{Path: path}, original, proposed), fs)
	if !errors.Is(err, ErrStaleFile) {
		t.Fatalf("err=%v", err)
	}
	got, readErr := os.ReadFile(path)
	if readErr != nil || !bytes.Equal(got, proposed) {
		t.Fatalf("competitor=%q err=%v", got, readErr)
	}
	entries, _ := os.ReadDir(dir)
	retained := 0
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".packages-apply-") {
			retained++
		}
	}
	if retained < 2 {
		t.Fatalf("recovery artifacts not retained: %v", entries)
	}
}

func TestApplyPrimarySwapbackFailureFallsBackDirectlyToRecovery(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "target")
	old := []byte("old\n")
	newb := []byte("new\n")
	_ = os.WriteFile(path, old, 0o600)
	fs := realApplyFilesystem()
	realExchange := fs.exchange
	calls := 0
	fs.exchange = func(a, b string) error {
		calls++
		if calls == 2 {
			return errors.New("primary swapback failed")
		}
		return realExchange(a, b)
	}
	fs.hook = func(stage applyStage, target, _ string) {
		if stage == applyBeforeGuardRecheck {
			_ = os.WriteFile(target, []byte("tampered\n"), 0o600)
		}
	}
	_, err := apply(ProposeReplacement(Target{Path: path}, old, newb), fs)
	if !errors.Is(err, ErrStaleFile) {
		t.Fatalf("err=%v", err)
	}
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, old) {
		t.Fatalf("target=%q calls=%d", got, calls)
	}
}

func TestApplyRecoveryExchangeFailureRetainsProposedAndRecovery(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "target")
	old := []byte("old\n")
	newb := []byte("new\n")
	_ = os.WriteFile(path, old, 0o600)
	fs := realApplyFilesystem()
	realExchange := fs.exchange
	calls := 0
	fs.exchange = func(a, b string) error {
		calls++
		if calls == 2 {
			return errors.New("recovery exchange failed")
		}
		return realExchange(a, b)
	}
	fs.hook = func(stage applyStage, _, oldTemp string) {
		if stage == applyBeforeGuardRecheck {
			_ = os.WriteFile(oldTemp, []byte("tampered-old\n"), 0o600)
		}
	}
	_, err := apply(ProposeReplacement(Target{Path: path}, old, newb), fs)
	if !errors.Is(err, ErrStaleFile) {
		t.Fatalf("err=%v", err)
	}
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, newb) {
		t.Fatalf("target=%q", got)
	}
	entries, _ := os.ReadDir(dir)
	retained := 0
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".packages-apply-") {
			retained++
		}
	}
	if retained < 2 {
		t.Fatalf("retained=%v", entries)
	}
}

func TestApplyClaimRejectsSymlinkSwapAndExistingGuard(t *testing.T) {
	for _, tc := range []struct {
		name string
		hook func(string, string)
	}{
		{"symlink swap", func(target, _ string) {
			backup := target + "-backup"
			_ = os.Rename(target, backup)
			_ = os.Symlink(backup, target)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "target")
			original := []byte("original\n")
			if err := os.WriteFile(path, original, 0o600); err != nil {
				t.Fatal(err)
			}
			fs := realApplyFilesystem()
			fs.hook = func(stage applyStage, target, guard string) {
				if stage == applyBeforeClaim {
					tc.hook(target, guard)
				}
			}
			_, err := apply(ProposeReplacement(Target{Path: path}, original, []byte("proposed\n")), fs)
			if err == nil {
				t.Fatal("claim conflict succeeded")
			}
		})
	}
}

func TestApplySurfacesCleanupFailureAndGuardPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "target")
	original := []byte("original\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("cleanup failed")
	fs := realApplyFilesystem()
	remove := fs.remove
	fs.remove = func(path string) error {
		if strings.Contains(path, "recovery") {
			return failure
		}
		return remove(path)
	}
	applied, err := apply(ProposeReplacement(Target{Path: path}, original, []byte("proposed\n")), fs)
	if !errors.Is(err, failure) || !strings.Contains(err.Error(), "recovery") {
		t.Fatalf("err=%v", err)
	}
	if applied.Path != path {
		t.Fatalf("committed edit not returned: %#v", applied)
	}
	got, readErr := os.ReadFile(path)
	if readErr != nil || string(got) != "proposed\n" {
		t.Fatalf("target=%q err=%v", got, readErr)
	}
}

func TestApplyRecoveryPreparationPreservesModeAndRejectsShortWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "target")
	old := []byte("old\n")
	_ = os.WriteFile(path, old, 0o644)
	fs := realApplyFilesystem()
	create := fs.createTemp
	calls := 0
	fs.createTemp = func(dir, pattern string) (applyTempFile, error) {
		calls++
		f, err := create(dir, pattern)
		if err != nil {
			return nil, err
		}
		if calls == 2 {
			return &shortWriteApplyTemp{applyTempFile: f}, nil
		}
		return f, nil
	}
	_, err := apply(ProposeReplacement(Target{Path: path}, old, []byte("new\n")), fs)
	if !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("err=%v", err)
	}
	info, _ := os.Stat(path)
	got, _ := os.ReadFile(path)
	if info.Mode().Perm() != 0o644 || !bytes.Equal(got, old) {
		t.Fatalf("mode=%o target=%q", info.Mode().Perm(), got)
	}
}

type shortWriteApplyTemp struct{ applyTempFile }

func (s *shortWriteApplyTemp) Write(p []byte) (int, error) {
	return s.applyTempFile.Write(p[:len(p)/2])
}

type concurrentEditApplyTempFile struct {
	applyTempFile
	onClose func() error
}

func (f *concurrentEditApplyTempFile) Close() error {
	if err := f.applyTempFile.Close(); err != nil {
		return err
	}
	return f.onClose()
}

type failingApplyTempFile struct {
	*os.File
	stage string
	err   error
}

func (f *failingApplyTempFile) Chmod(mode os.FileMode) error {
	if f.stage == "chmod" {
		return f.err
	}
	return f.File.Chmod(mode)
}

func (f *failingApplyTempFile) Write(content []byte) (int, error) {
	if f.stage == "write" {
		written, _ := f.File.Write(content[:len(content)/2])
		return written, f.err
	}
	return f.File.Write(content)
}

func (f *failingApplyTempFile) Sync() error {
	if f.stage == "sync" {
		return f.err
	}
	return f.File.Sync()
}

func (f *failingApplyTempFile) Close() error {
	err := f.File.Close()
	if f.stage == "close" {
		return f.err
	}
	return err
}

func assertNoApplyTemps(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".packages-apply-") {
			t.Fatalf("temporary file leaked: %s", entry.Name())
		}
	}
}
