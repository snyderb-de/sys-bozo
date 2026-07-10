package packages

import (
	"bytes"
	"crypto/sha256"
	"errors"
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
				filesystem.rename = func(_, _ string) error { return failure }
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
