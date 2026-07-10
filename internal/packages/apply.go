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

func apply(proposal Proposal, filesystem applyFilesystem) (AppliedEdit, error) {
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
	tempPath, proposedInfo, err := prepareApplyFile(filesystem, filepath.Dir(proposal.Target.Path), applyTempPattern, after, info.Mode())
	if err != nil {
		return AppliedEdit{}, err
	}
	guardPath, recoveryInfo, err := prepareApplyFile(filesystem, filepath.Dir(proposal.Target.Path), applyTempPattern+"-recovery", current, info.Mode())
	if err != nil {
		return AppliedEdit{}, errors.Join(err, cleanupApplyArtifacts(filesystem, filepath.Dir(proposal.Target.Path), tempPath))
	}
	state := exchangeApplyState{filesystem: filesystem, target: proposal.Target.Path, oldTemp: tempPath, recovery: guardPath, originalInfo: info, proposedInfo: proposedInfo, recoveryInfo: recoveryInfo, originalHash: proposal.OriginalHash, proposedHash: proposal.ProposedHash, originalMode: info.Mode()}
	runApplyHook(filesystem, applyBeforeClaim, proposal.Target.Path, guardPath)
	latestInfo, latestInfoErr := filesystem.stat(proposal.Target.Path)
	latest, latestErr := filesystem.readFile(proposal.Target.Path)
	if latestInfoErr != nil || latestErr != nil || !latestInfo.Mode().IsRegular() || !filesystem.sameFile(info, latestInfo) || sha256.Sum256(latest) != proposal.OriginalHash {
		return AppliedEdit{}, errors.Join(ErrStaleFile, latestInfoErr, latestErr, cleanupApplyArtifacts(filesystem, filepath.Dir(proposal.Target.Path), tempPath, guardPath))
	}
	if err := filesystem.exchange(proposal.Target.Path, tempPath); err != nil {
		return AppliedEdit{}, errors.Join(fmt.Errorf("atomically exchange declaration target: %w", err), cleanupApplyArtifacts(filesystem, filepath.Dir(proposal.Target.Path), tempPath, guardPath))
	}
	runApplyHook(filesystem, applyAfterClaim, proposal.Target.Path, tempPath)
	runApplyHook(filesystem, applyBeforeInstall, proposal.Target.Path, tempPath)
	runApplyHook(filesystem, applyBeforeGuardRecheck, proposal.Target.Path, tempPath)
	targetValid := state.valid(state.target, state.proposedInfo, state.proposedHash)
	oldValid := state.valid(state.oldTemp, state.originalInfo, state.originalHash)
	if !oldValid || !targetValid {
		return AppliedEdit{}, state.rollback(oldValid)
	}
	if err := filesystem.syncDir(filepath.Dir(proposal.Target.Path)); err != nil {
		return completedEdit(proposal, current, after, beforeHash), fmt.Errorf("sync declaration cleanup: %w", err)
	}
	if err := cleanupApplyArtifacts(filesystem, filepath.Dir(proposal.Target.Path), tempPath, guardPath); err != nil {
		return completedEdit(proposal, current, after, beforeHash), err
	}
	return completedEdit(proposal, current, after, beforeHash), nil
}

type exchangeApplyState struct {
	filesystem                               applyFilesystem
	target, oldTemp, recovery                string
	originalInfo, proposedInfo, recoveryInfo os.FileInfo
	originalHash, proposedHash               [32]byte
	originalMode                             os.FileMode
}

func (s exchangeApplyState) valid(path string, identity os.FileInfo, hash [32]byte) bool {
	if !s.identity(path, identity) {
		return false
	}
	b, err := s.filesystem.readFile(path)
	return err == nil && sha256.Sum256(b) == hash
}

func (s exchangeApplyState) identity(path string, identity os.FileInfo) bool {
	info, err := s.filesystem.stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != s.originalMode.Perm() || !s.filesystem.sameFile(info, identity) {
		return false
	}
	return true
}

func (s exchangeApplyState) rollback(oldValid bool) error {
	if !s.identity(s.target, s.proposedInfo) {
		return fmt.Errorf("%w: competing target preserved at %s; original artifacts retained at %s and %s", ErrStaleFile, s.target, s.oldTemp, s.recovery)
	}
	source := s.recovery
	identity := s.recoveryInfo
	if oldValid {
		source = s.oldTemp
		identity = s.originalInfo
	} else if !s.valid(s.recovery, s.recoveryInfo, s.originalHash) {
		return fmt.Errorf("%w: invalid recovery; artifacts retained at %s and %s", ErrStaleFile, s.oldTemp, s.recovery)
	}
	if err := s.filesystem.exchange(s.target, source); err != nil {
		if oldValid && s.valid(s.recovery, s.recoveryInfo, s.originalHash) {
			if recoveryErr := s.filesystem.exchange(s.target, s.recovery); recoveryErr == nil {
				if s.valid(s.target, s.recoveryInfo, s.originalHash) {
					return errors.Join(ErrStaleFile, cleanupApplyArtifacts(s.filesystem, filepath.Dir(s.target), s.oldTemp, s.recovery))
				}
			} else {
				err = errors.Join(err, recoveryErr)
			}
		}
		return fmt.Errorf("%w: rollback exchange failed; recovery retained at %s and %s: %v", ErrStaleFile, s.oldTemp, s.recovery, err)
	}
	if !s.valid(s.target, identity, s.originalHash) {
		return fmt.Errorf("%w: rollback could not be verified; recovery retained at %s and %s", ErrStaleFile, s.oldTemp, s.recovery)
	}
	return errors.Join(ErrStaleFile, cleanupApplyArtifacts(s.filesystem, filepath.Dir(s.target), s.oldTemp, s.recovery))
}

func prepareApplyFile(fs applyFilesystem, dir, pattern string, content []byte, mode os.FileMode) (string, os.FileInfo, error) {
	f, err := fs.createTemp(dir, pattern)
	if err != nil {
		return "", nil, err
	}
	path := f.Name()
	fail := func(e error) (string, os.FileInfo, error) {
		_ = f.Close()
		return "", nil, errors.Join(e, cleanupApplyArtifacts(fs, dir, path))
	}
	if err := f.Chmod(mode); err != nil {
		return fail(err)
	}
	n, err := f.Write(content)
	if err != nil {
		return fail(err)
	}
	if n != len(content) {
		return fail(io.ErrShortWrite)
	}
	if err := f.Sync(); err != nil {
		return fail(err)
	}
	if err := f.Close(); err != nil {
		return "", nil, errors.Join(err, cleanupApplyArtifacts(fs, dir, path))
	}
	info, err := fs.stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != mode.Perm() {
		return "", nil, errors.Join(fmt.Errorf("invalid prepared file %s", path), err, cleanupApplyArtifacts(fs, dir, path))
	}
	b, err := fs.readFile(path)
	if err != nil || !bytes.Equal(b, content) {
		return "", nil, errors.Join(fmt.Errorf("prepared file content mismatch %s", path), err, cleanupApplyArtifacts(fs, dir, path))
	}
	return path, info, nil
}

func cleanupApplyArtifacts(fs applyFilesystem, dir string, paths ...string) error {
	var result error
	for _, p := range paths {
		if p == "" {
			continue
		}
		if err := fs.remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			result = errors.Join(result, fmt.Errorf("remove artifact %s: %w", p, err))
		}
	}
	if err := fs.syncDir(dir); err != nil {
		result = errors.Join(result, fmt.Errorf("sync artifact cleanup: %w", err))
	}
	return result
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
