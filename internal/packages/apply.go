package packages

import (
	"bytes"
	"crypto/sha256"
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
}

func realApplyFilesystem() applyFilesystem {
	return applyFilesystem{
		readFile: os.ReadFile,
		stat:     os.Stat,
		createTemp: func(dir, pattern string) (applyTempFile, error) {
			return os.CreateTemp(dir, pattern)
		},
		remove: os.Remove,
		rename: os.Rename,
	}
}

func apply(proposal Proposal, filesystem applyFilesystem) (AppliedEdit, error) {
	current, err := filesystem.readFile(proposal.Target.Path)
	if err != nil {
		return AppliedEdit{}, fmt.Errorf("read declaration file: %w", err)
	}
	beforeHash := sha256.Sum256(current)
	if beforeHash != proposal.OriginalHash {
		return AppliedEdit{}, ErrStaleFile
	}

	info, err := filesystem.stat(proposal.Target.Path)
	if err != nil {
		return AppliedEdit{}, fmt.Errorf("stat declaration file: %w", err)
	}
	after := bytes.Clone(proposal.Proposed)
	temp, err := filesystem.createTemp(filepath.Dir(proposal.Target.Path), applyTempPattern)
	if err != nil {
		return AppliedEdit{}, fmt.Errorf("create temporary declaration file: %w", err)
	}
	tempPath := temp.Name()
	defer filesystem.remove(tempPath)

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

	latest, err := filesystem.readFile(proposal.Target.Path)
	if err != nil {
		return AppliedEdit{}, fmt.Errorf("recheck declaration file: %w", err)
	}
	if sha256.Sum256(latest) != proposal.OriginalHash {
		return AppliedEdit{}, ErrStaleFile
	}
	if err := filesystem.rename(tempPath, proposal.Target.Path); err != nil {
		return AppliedEdit{}, fmt.Errorf("replace declaration file: %w", err)
	}

	return AppliedEdit{
		Path:       proposal.Target.Path,
		Before:     bytes.Clone(current),
		After:      after,
		BeforeHash: beforeHash,
		AfterHash:  sha256.Sum256(after),
	}, nil
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
