package packages

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const applyTempPattern = ".packages-apply-*"

func Apply(proposal Proposal) (AppliedEdit, error) {
	return apply(proposal, realApplyFilesystem())
}

type applyTempFile interface {
	Name() string
	Chmod(os.FileMode) error
	Write([]byte) (int, error)
	Sync() error
	Close() error
}

type applyFilesystem struct {
	readFile   func(string) ([]byte, error)
	stat       func(string) (os.FileInfo, error)
	createTemp func(string, string) (applyTempFile, error)
	remove     func(string) error
	rename     func(string, string) error
	link       func(string, string) error
	sameFile   func(os.FileInfo, os.FileInfo) bool
	syncDir    func(string) error
	hook       func(applyStage, string, string)
}

type applyStage uint8

const (
	applyBeforeClaim applyStage = iota
	applyAfterClaim
	applyBeforeInstall
	applyBeforeGuardRecheck
)

func realApplyFilesystem() applyFilesystem {
	return applyFilesystem{
		readFile: os.ReadFile,
		stat:     os.Lstat,
		createTemp: func(dir, pattern string) (applyTempFile, error) {
			return os.CreateTemp(dir, pattern)
		},
		remove:   os.Remove,
		rename:   os.Rename,
		link:     os.Link,
		sameFile: os.SameFile,
		syncDir: func(dir string) error {
			file, err := os.Open(dir)
			if err != nil {
				return err
			}
			return errors.Join(file.Sync(), file.Close())
		},
	}
}

func apply(proposal Proposal, filesystem applyFilesystem) (applied AppliedEdit, retErr error) {
	info, err := filesystem.stat(proposal.Target.Path)
	if err != nil {
		return AppliedEdit{}, fmt.Errorf("lstat declaration file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return AppliedEdit{}, fmt.Errorf("declaration target must be a regular non-symlink file: %s", proposal.Target.Path)
	}
	current, err := filesystem.readFile(proposal.Target.Path)
	if err != nil {
		return AppliedEdit{}, fmt.Errorf("read declaration file: %w", err)
	}
	beforeHash := sha256.Sum256(current)
	if beforeHash != proposal.OriginalHash {
		return AppliedEdit{}, ErrStaleFile
	}

	after := bytes.Clone(proposal.Proposed)
	temp, err := filesystem.createTemp(filepath.Dir(proposal.Target.Path), applyTempPattern)
	if err != nil {
		return AppliedEdit{}, fmt.Errorf("create temporary declaration file: %w", err)
	}
	tempPath := temp.Name()
	defer func() {
		if err := filesystem.remove(tempPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			retErr = errors.Join(retErr, fmt.Errorf("remove proposal temporary file %q: %w", tempPath, err))
		}
	}()

	if err := temp.Chmod(info.Mode()); err != nil {
		_ = temp.Close()
		return AppliedEdit{}, fmt.Errorf("set temporary declaration permissions: %w", err)
	}
	if written, err := temp.Write(after); err != nil {
		_ = temp.Close()
		return AppliedEdit{}, fmt.Errorf("write temporary declaration file: %w", err)
	} else if written != len(after) {
		_ = temp.Close()
		return AppliedEdit{}, fmt.Errorf("write temporary declaration file: %w", io.ErrShortWrite)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return AppliedEdit{}, fmt.Errorf("sync temporary declaration file: %w", err)
	}
	if err := temp.Close(); err != nil {
		return AppliedEdit{}, fmt.Errorf("close temporary declaration file: %w", err)
	}

	guardPath := tempPath + ".guard"
	runApplyHook(filesystem, applyBeforeClaim, proposal.Target.Path, guardPath)
	if err := filesystem.rename(proposal.Target.Path, guardPath); err != nil {
		return AppliedEdit{}, fmt.Errorf("claim declaration target: %w", err)
	}
	runApplyHook(filesystem, applyAfterClaim, proposal.Target.Path, guardPath)
	if err := filesystem.syncDir(filepath.Dir(proposal.Target.Path)); err != nil {
		restoreErr := restoreGuard(filesystem, guardPath, proposal.Target.Path)
		return AppliedEdit{}, errors.Join(fmt.Errorf("sync claimed declaration directory: %w", err), restoreErr)
	}

	guarded, err := filesystem.readFile(guardPath)
	if err != nil {
		return AppliedEdit{}, errors.Join(fmt.Errorf("read claimed declaration file: %w", err), restoreGuard(filesystem, guardPath, proposal.Target.Path))
	}
	if sha256.Sum256(guarded) != proposal.OriginalHash {
		return AppliedEdit{}, errors.Join(staleConflict("claimed file hash changed", guardPath), restoreGuard(filesystem, guardPath, proposal.Target.Path))
	}

	runApplyHook(filesystem, applyBeforeInstall, proposal.Target.Path, guardPath)
	if err := filesystem.link(tempPath, proposal.Target.Path); err != nil {
		if errors.Is(err, os.ErrExist) {
			return AppliedEdit{}, staleConflict("competing target appeared before install", guardPath)
		}
		return AppliedEdit{}, errors.Join(fmt.Errorf("install declaration file without overwrite: %w", err), restoreGuard(filesystem, guardPath, proposal.Target.Path))
	}
	if err := filesystem.syncDir(filepath.Dir(proposal.Target.Path)); err != nil {
		return AppliedEdit{}, errors.Join(fmt.Errorf("sync installed declaration directory: %w", err), rollbackInstalled(filesystem, tempPath, guardPath, proposal.Target.Path))
	}

	// A writer holding the old inode can still mutate guardPath after claim.
	// Recheck immediately before releasing it. Portable APIs cannot prevent a
	// non-cooperative old-inode write that begins after this final recheck; such
	// a write cannot overwrite the newly installed target directory entry.
	runApplyHook(filesystem, applyBeforeGuardRecheck, proposal.Target.Path, guardPath)
	guarded, err = filesystem.readFile(guardPath)
	if err != nil || sha256.Sum256(guarded) != proposal.OriginalHash {
		rollbackErr := rollbackInstalled(filesystem, tempPath, guardPath, proposal.Target.Path)
		if err != nil {
			return AppliedEdit{}, errors.Join(staleConflict("could not recheck claimed file", guardPath), err, rollbackErr)
		}
		return AppliedEdit{}, errors.Join(staleConflict("old inode changed after claim", guardPath), rollbackErr)
	}
	if err := filesystem.remove(guardPath); err != nil {
		return AppliedEdit{}, fmt.Errorf("remove claimed original %q: %w", guardPath, err)
	}
	if err := filesystem.syncDir(filepath.Dir(proposal.Target.Path)); err != nil {
		return AppliedEdit{}, fmt.Errorf("sync declaration cleanup: %w", err)
	}

	return AppliedEdit{
		Path:       proposal.Target.Path,
		Before:     bytes.Clone(current),
		After:      after,
		BeforeHash: beforeHash,
		AfterHash:  sha256.Sum256(after),
	}, nil
}

func runApplyHook(filesystem applyFilesystem, stage applyStage, target, guard string) {
	if filesystem.hook != nil {
		filesystem.hook(stage, target, guard)
	}
}

func staleConflict(detail, guard string) error {
	return fmt.Errorf("%w: %s; claimed original retained at %s if automatic restore is unsafe", ErrStaleFile, detail, guard)
}

func restoreGuard(filesystem applyFilesystem, guard, target string) error {
	if err := filesystem.link(guard, target); err != nil {
		if errors.Is(err, os.ErrExist) {
			return staleConflict("competing target prevents original restore", guard)
		}
		return fmt.Errorf("restore claimed original from %q: %w", guard, err)
	}
	if err := filesystem.remove(guard); err != nil {
		return fmt.Errorf("remove restored guard %q: %w", guard, err)
	}
	return filesystem.syncDir(filepath.Dir(target))
}

func rollbackInstalled(filesystem applyFilesystem, proposed, guard, target string) error {
	targetInfo, targetErr := filesystem.stat(target)
	proposedInfo, proposedErr := filesystem.stat(proposed)
	if targetErr == nil && proposedErr == nil && filesystem.sameFile(targetInfo, proposedInfo) {
		if err := filesystem.remove(target); err != nil {
			return fmt.Errorf("remove installed proposal during rollback: %w", err)
		}
	} else if targetErr == nil {
		return staleConflict("competing target prevents rollback", guard)
	} else if !errors.Is(targetErr, os.ErrNotExist) {
		return fmt.Errorf("inspect installed proposal during rollback: %w", targetErr)
	}
	return restoreGuard(filesystem, guard, target)
}

func ProposeRevert(applied AppliedEdit) (Proposal, error) {
	current, err := os.ReadFile(applied.Path)
	if err != nil {
		return Proposal{}, fmt.Errorf("read declaration file for revert: %w", err)
	}
	currentHash := sha256.Sum256(current)
	if currentHash != applied.AfterHash {
		return Proposal{}, ErrStaleFile
	}

	return ProposeReplacement(Target{Path: applied.Path}, current, applied.Before), nil
}

func ProposeReplacement(target Target, original, proposed []byte) Proposal {
	original = bytes.Clone(original)
	proposed = bytes.Clone(proposed)
	return Proposal{
		Target:       target,
		Original:     original,
		Proposed:     proposed,
		OriginalHash: sha256.Sum256(original),
		ProposedHash: sha256.Sum256(proposed),
		Diff:         unifiedReplacementDiff(original, proposed),
	}
}

func unifiedReplacementDiff(original, proposed []byte) string {
	originalLines := splitLines(original)
	proposedLines := splitLines(proposed)
	var diff strings.Builder
	diff.WriteString("--- original\n")
	diff.WriteString("+++ proposed\n")
	diff.WriteString("@@ -1,")
	diff.WriteString(strconv.Itoa(len(originalLines)))
	diff.WriteString(" +1,")
	diff.WriteString(strconv.Itoa(len(proposedLines)))
	diff.WriteString(" @@\n")
	for _, line := range originalLines {
		writeDiffLine(&diff, '-', line)
	}
	for _, line := range proposedLines {
		writeDiffLine(&diff, '+', line)
	}
	return diff.String()
}
