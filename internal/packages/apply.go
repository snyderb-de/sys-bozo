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
	exchange   func(string, string) error
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
		exchange: exchangePaths,
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
	proposedInfo, err := filesystem.stat(tempPath)
	if err != nil {
		return AppliedEdit{}, fmt.Errorf("stat proposed temporary file: %w", err)
	}

	guard, err := filesystem.createTemp(filepath.Dir(proposal.Target.Path), applyTempPattern+"-recovery")
	if err != nil {
		return AppliedEdit{}, fmt.Errorf("create recovery file: %w", err)
	}
	guardPath := guard.Name()
	if _, err := guard.Write(current); err != nil {
		_ = guard.Close()
		return AppliedEdit{}, fmt.Errorf("write recovery file: %w", err)
	}
	if err := guard.Sync(); err != nil {
		_ = guard.Close()
		return AppliedEdit{}, fmt.Errorf("sync recovery file: %w", err)
	}
	if err := guard.Close(); err != nil {
		return AppliedEdit{}, fmt.Errorf("close recovery file: %w", err)
	}
	defer func() {
		if err := filesystem.remove(guardPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			retErr = errors.Join(retErr, fmt.Errorf("remove recovery file %q: %w", guardPath, err))
		}
	}()
	runApplyHook(filesystem, applyBeforeClaim, proposal.Target.Path, guardPath)
	latestInfo, latestInfoErr := filesystem.stat(proposal.Target.Path)
	latest, latestErr := filesystem.readFile(proposal.Target.Path)
	if latestInfoErr != nil || latestErr != nil || !latestInfo.Mode().IsRegular() || !filesystem.sameFile(info, latestInfo) || sha256.Sum256(latest) != proposal.OriginalHash {
		return AppliedEdit{}, errors.Join(ErrStaleFile, latestInfoErr, latestErr)
	}
	if err := filesystem.exchange(proposal.Target.Path, tempPath); err != nil {
		return AppliedEdit{}, fmt.Errorf("atomically exchange declaration target: %w", err)
	}
	runApplyHook(filesystem, applyAfterClaim, proposal.Target.Path, tempPath)
	runApplyHook(filesystem, applyBeforeInstall, proposal.Target.Path, tempPath)
	runApplyHook(filesystem, applyBeforeGuardRecheck, proposal.Target.Path, tempPath)
	guarded, err := filesystem.readFile(tempPath)
	targetInfo, targetInfoErr := filesystem.stat(proposal.Target.Path)
	tempInfo, tempInfoErr := filesystem.stat(tempPath)
	installed, installedErr := filesystem.readFile(proposal.Target.Path)
	targetValid := targetInfoErr == nil && filesystem.sameFile(targetInfo, proposedInfo) && installedErr == nil && sha256.Sum256(installed) == proposal.ProposedHash
	oldValid := tempInfoErr == nil && tempInfo.Mode().IsRegular() && filesystem.sameFile(info, tempInfo) && err == nil && sha256.Sum256(guarded) == proposal.OriginalHash
	if !oldValid || !targetValid {
		rollbackErr := filesystem.exchange(proposal.Target.Path, tempPath)
		restored, readErr := filesystem.readFile(proposal.Target.Path)
		if readErr != nil || sha256.Sum256(restored) != proposal.OriginalHash {
			rollbackErr = errors.Join(rollbackErr, filesystem.exchange(proposal.Target.Path, guardPath))
		}
		return AppliedEdit{}, errors.Join(staleConflict("atomic exchange validation conflict", guardPath), targetInfoErr, tempInfoErr, installedErr, err, rollbackErr)
	}
	if err := filesystem.syncDir(filepath.Dir(proposal.Target.Path)); err != nil {
		return completedEdit(proposal, current, after, beforeHash), fmt.Errorf("sync declaration cleanup: %w", err)
	}
	return completedEdit(proposal, current, after, beforeHash), nil
}

func completedEdit(proposal Proposal, current, after []byte, beforeHash [32]byte) AppliedEdit {
	return AppliedEdit{
		Path:       proposal.Target.Path,
		Before:     bytes.Clone(current),
		After:      after,
		BeforeHash: beforeHash,
		AfterHash:  sha256.Sum256(after),
	}
}

func runApplyHook(filesystem applyFilesystem, stage applyStage, target, guard string) {
	if filesystem.hook != nil {
		filesystem.hook(stage, target, guard)
	}
}

func staleConflict(detail, guard string) error {
	return fmt.Errorf("%w: %s; claimed original retained at %s if automatic restore is unsafe", ErrStaleFile, detail, guard)
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
