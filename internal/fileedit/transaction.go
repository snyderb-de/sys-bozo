package fileedit

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const repoLockName = "sys-bozo.lock"

type transactionStage uint8

const (
	transactionAfterPrepareAll transactionStage = iota
	transactionBeforeCommit
	transactionBeforeRollback
)

var transactionHook func(transactionStage, string)

func ResolveRepoLockPath(repo string) (string, error) {
	if !filepath.IsAbs(repo) {
		return "", errors.New("repository path must be absolute")
	}
	realRepo, err := filepath.EvalSymlinks(filepath.Clean(repo))
	if err != nil {
		return "", fmt.Errorf("resolve repository path: %w", err)
	}
	info, err := os.Lstat(realRepo)
	if err != nil || !info.IsDir() {
		return "", errors.Join(errors.New("repository path must be a directory"), err)
	}
	gitMetadata := filepath.Join(realRepo, ".git")
	gitInfo, err := os.Lstat(gitMetadata)
	if err != nil {
		return "", fmt.Errorf("inspect repository Git metadata: %w", err)
	}
	var commonDir string
	switch {
	case gitInfo.IsDir():
		commonDir = gitMetadata
	case gitInfo.Mode().IsRegular():
		gitDir, err := readGitPathFile(gitMetadata, "gitdir:", realRepo)
		if err != nil {
			return "", err
		}
		gitDirInfo, err := os.Lstat(gitDir)
		if err != nil || !gitDirInfo.IsDir() {
			return "", errors.Join(errors.New("worktree Git directory must be a non-symlink directory"), err)
		}
		commonDir, err = readGitPathFile(filepath.Join(gitDir, "commondir"), "", gitDir)
		if err != nil {
			return "", err
		}
	default:
		return "", errors.New("repository .git metadata must be a non-symlink directory or regular gitfile")
	}
	commonMetadataInfo, err := os.Lstat(commonDir)
	if err != nil || !commonMetadataInfo.IsDir() {
		return "", errors.Join(errors.New("common Git directory must be a non-symlink directory"), err)
	}
	realCommon, err := filepath.EvalSymlinks(commonDir)
	if err != nil {
		return "", fmt.Errorf("resolve common Git directory: %w", err)
	}
	commonInfo, err := os.Lstat(realCommon)
	if err != nil || !commonInfo.IsDir() {
		return "", errors.Join(errors.New("common Git directory must be a directory"), err)
	}
	return filepath.Join(realCommon, repoLockName), nil
}

func readGitPathFile(path, prefix, relativeTo string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.Join(fmt.Errorf("Git metadata path file must be a non-symlink regular file: %s", path), err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read Git metadata path file: %w", err)
	}
	value := strings.TrimSpace(string(data))
	if prefix != "" {
		if !strings.HasPrefix(value, prefix) {
			return "", fmt.Errorf("invalid Git metadata path file: %s", path)
		}
		value = strings.TrimSpace(strings.TrimPrefix(value, prefix))
	}
	if value == "" || strings.ContainsAny(value, "\r\n\x00") {
		return "", fmt.Errorf("invalid Git metadata path file: %s", path)
	}
	if !filepath.IsAbs(value) {
		value = filepath.Join(relativeTo, value)
	}
	return filepath.Clean(value), nil
}

func ApplyTransaction(proposal TransactionProposal) (result TransactionResult, err error) {
	normalized, err := validateTransaction(proposal)
	if err != nil {
		return TransactionResult{}, err
	}
	release, err := acquireRepoLock(normalized.LockPath)
	if err != nil {
		return TransactionResult{}, err
	}
	defer func() { err = errors.Join(err, release()) }()

	prepared := make([]*PreparedEdit, 0, len(normalized.Edits))
	for _, edit := range normalized.Edits {
		item, prepareErr := Prepare(edit)
		if prepareErr != nil {
			return TransactionResult{}, errors.Join(prepareErr, abortPrepared(prepared))
		}
		prepared = append(prepared, item)
	}
	runTransactionHook(transactionAfterPrepareAll, "")

	applied := make([]AppliedEdit, 0, len(prepared))
	for i, item := range prepared {
		path := normalized.Edits[i].Path
		runTransactionHook(transactionBeforeCommit, path)
		edit, commitErr := item.Commit()
		if commitErr == nil {
			applied = append(applied, edit)
			continue
		}
		if edit.Path != "" {
			applied = append(applied, edit)
		}
		abortErr := abortPrepared(prepared[i+1:])
		unresolved, recovery, rollbackErr := rollbackApplied(applied)
		return cloneTransactionResult(TransactionResult{Edits: unresolved, RecoveryPaths: recovery}), errors.Join(commitErr, abortErr, rollbackErr)
	}
	return cloneTransactionResult(TransactionResult{Edits: applied}), nil
}

func validateTransaction(proposal TransactionProposal) (TransactionProposal, error) {
	if !filepath.IsAbs(proposal.Repo) || !filepath.IsAbs(proposal.LockPath) {
		return TransactionProposal{}, errors.New("repository and lock paths must be absolute")
	}
	realRepo, err := filepath.EvalSymlinks(filepath.Clean(proposal.Repo))
	if err != nil {
		return TransactionProposal{}, fmt.Errorf("resolve repository path: %w", err)
	}
	wantLock, err := ResolveRepoLockPath(realRepo)
	if err != nil {
		return TransactionProposal{}, err
	}
	lockParent, err := filepath.EvalSymlinks(filepath.Dir(filepath.Clean(proposal.LockPath)))
	if err != nil || filepath.Join(lockParent, filepath.Base(proposal.LockPath)) != wantLock {
		return TransactionProposal{}, errors.Join(errors.New("lock path is not the repository common Git lock"), err)
	}
	edits := make([]Proposal, 0, len(proposal.Edits))
	seen := make(map[string]struct{}, len(proposal.Edits))
	for _, edit := range proposal.Edits {
		if !Valid(edit) || !filepath.IsAbs(edit.Path) {
			return TransactionProposal{}, fmt.Errorf("invalid file proposal: %s", edit.Path)
		}
		parent, err := filepath.EvalSymlinks(filepath.Dir(filepath.Clean(edit.Path)))
		if err != nil {
			return TransactionProposal{}, fmt.Errorf("resolve proposal parent: %w", err)
		}
		normalizedPath := filepath.Join(parent, filepath.Base(edit.Path))
		relative, err := filepath.Rel(realRepo, normalizedPath)
		if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return TransactionProposal{}, errors.Join(fmt.Errorf("proposal target is outside repository: %s", edit.Path), err)
		}
		info, err := os.Lstat(normalizedPath)
		if err != nil || !info.Mode().IsRegular() {
			return TransactionProposal{}, errors.Join(fmt.Errorf("proposal target must be a regular non-symlink file: %s", edit.Path), err)
		}
		if _, duplicate := seen[normalizedPath]; duplicate {
			return TransactionProposal{}, fmt.Errorf("duplicate transaction target: %s", normalizedPath)
		}
		seen[normalizedPath] = struct{}{}
		copy := cloneProposal(edit)
		copy.Path = normalizedPath
		edits = append(edits, copy)
	}
	sort.Slice(edits, func(i, j int) bool { return edits[i].Path < edits[j].Path })
	return TransactionProposal{Repo: realRepo, LockPath: wantLock, Edits: edits}, nil
}

func rollbackApplied(applied []AppliedEdit) ([]AppliedEdit, []string, error) {
	var unresolved []AppliedEdit
	var recovery []string
	var rollbackErr error
	for i := len(applied) - 1; i >= 0; i-- {
		edit := applied[i]
		runTransactionHook(transactionBeforeRollback, edit.Path)
		if err := applyPreparedRevert(edit); err != nil {
			unresolved = append(unresolved, cloneAppliedEdit(edit))
			recovery = append(recovery, edit.Path)
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("rollback %s: %w", edit.Path, err))
		}
	}
	sort.Slice(unresolved, func(i, j int) bool { return unresolved[i].Path < unresolved[j].Path })
	sort.Strings(recovery)
	return unresolved, recovery, rollbackErr
}

func applyPreparedRevert(edit AppliedEdit) error {
	proposal, err := ProposeRevert(edit)
	if err != nil {
		return err
	}
	prepared, err := Prepare(proposal)
	if err != nil {
		return err
	}
	_, err = prepared.Commit()
	return err
}

func abortPrepared(prepared []*PreparedEdit) error {
	var err error
	for i := len(prepared) - 1; i >= 0; i-- {
		err = errors.Join(err, prepared[i].Abort())
	}
	return err
}

func ProposeTransactionRevert(result TransactionResult, repo, lockPath string) (TransactionProposal, error) {
	edits := make([]Proposal, 0, len(result.Edits))
	for _, edit := range result.Edits {
		proposal, err := ProposeRevert(cloneAppliedEdit(edit))
		if err != nil {
			return TransactionProposal{}, err
		}
		edits = append(edits, proposal)
	}
	return validateTransaction(TransactionProposal{Repo: repo, LockPath: lockPath, Edits: edits})
}

func runTransactionHook(stage transactionStage, path string) {
	if transactionHook != nil {
		transactionHook(stage, path)
	}
}

func cloneProposal(proposal Proposal) Proposal {
	proposal.Original = bytes.Clone(proposal.Original)
	proposal.Proposed = bytes.Clone(proposal.Proposed)
	return proposal
}

func cloneAppliedEdit(edit AppliedEdit) AppliedEdit {
	edit.Before = bytes.Clone(edit.Before)
	edit.After = bytes.Clone(edit.After)
	return edit
}

func cloneTransactionResult(result TransactionResult) TransactionResult {
	clone := TransactionResult{Edits: make([]AppliedEdit, len(result.Edits)), RecoveryPaths: append([]string(nil), result.RecoveryPaths...)}
	for i := range result.Edits {
		clone.Edits[i] = cloneAppliedEdit(result.Edits[i])
	}
	return clone
}
