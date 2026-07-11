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
	transactionBeforeLock
	transactionBeforePrepare
	transactionBeforeCommit
	transactionBeforeRollback
)

var transactionHook func(transactionStage, string)
var transactionPrepare = Prepare

type pathIdentity struct {
	path string
	info os.FileInfo
}

type resolvedRepoLock struct {
	path      string
	directory pathIdentity
}

type validatedTransaction struct {
	proposal TransactionProposal
	lock     resolvedRepoLock
	parents  map[string]pathIdentity
}

type preparedTransactionEdit struct {
	path     string
	parent   pathIdentity
	prepared *PreparedEdit
}

func ResolveRepoLockPath(repo string) (string, error) {
	lock, err := resolveRepoLock(repo)
	return lock.path, err
}

func resolveRepoLock(repo string) (resolvedRepoLock, error) {
	if !filepath.IsAbs(repo) {
		return resolvedRepoLock{}, errors.New("repository path must be absolute")
	}
	realRepo, err := filepath.EvalSymlinks(filepath.Clean(repo))
	if err != nil {
		return resolvedRepoLock{}, fmt.Errorf("resolve repository path: %w", err)
	}
	info, err := os.Lstat(realRepo)
	if err != nil || !info.IsDir() {
		return resolvedRepoLock{}, errors.Join(errors.New("repository path must be a directory"), err)
	}
	gitMetadata := filepath.Join(realRepo, ".git")
	gitInfo, err := os.Lstat(gitMetadata)
	if err != nil {
		return resolvedRepoLock{}, fmt.Errorf("inspect repository Git metadata: %w", err)
	}
	var commonDir string
	switch {
	case gitInfo.IsDir():
		commonDir = gitMetadata
	case gitInfo.Mode().IsRegular():
		gitDir, err := readGitPathFile(gitMetadata, "gitdir:", realRepo)
		if err != nil {
			return resolvedRepoLock{}, err
		}
		gitDirInfo, err := os.Lstat(gitDir)
		if err != nil || !gitDirInfo.IsDir() {
			return resolvedRepoLock{}, errors.Join(errors.New("worktree Git directory must be a non-symlink directory"), err)
		}
		commonDir, err = readGitPathFile(filepath.Join(gitDir, "commondir"), "", gitDir)
		if err != nil {
			return resolvedRepoLock{}, err
		}
	default:
		return resolvedRepoLock{}, errors.New("repository .git metadata must be a non-symlink directory or regular gitfile")
	}
	commonMetadataInfo, err := os.Lstat(commonDir)
	if err != nil || !commonMetadataInfo.IsDir() {
		return resolvedRepoLock{}, errors.Join(errors.New("common Git directory must be a non-symlink directory"), err)
	}
	realCommon, err := filepath.EvalSymlinks(commonDir)
	if err != nil {
		return resolvedRepoLock{}, fmt.Errorf("resolve common Git directory: %w", err)
	}
	commonInfo, err := os.Lstat(realCommon)
	if err != nil || !commonInfo.IsDir() {
		return resolvedRepoLock{}, errors.Join(errors.New("common Git directory must be a directory"), err)
	}
	return resolvedRepoLock{path: filepath.Join(realCommon, repoLockName), directory: pathIdentity{path: realCommon, info: commonInfo}}, nil
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
	runTransactionHook(transactionBeforeLock, normalized.lock.path)
	release, err := acquireRepoLock(normalized.lock)
	if err != nil {
		return TransactionResult{}, err
	}
	defer func() { err = errors.Join(err, release()) }()

	prepared := make([]preparedTransactionEdit, 0, len(normalized.proposal.Edits))
	for _, edit := range normalized.proposal.Edits {
		parent := normalized.parents[edit.Path]
		runTransactionHook(transactionBeforePrepare, edit.Path)
		if prepareErr := revalidatePathIdentity(parent, "prepare transaction edit"); prepareErr != nil {
			abortErr, recovery := abortPrepared(prepared)
			return TransactionResult{RecoveryPaths: recovery}, errors.Join(prepareErr, abortErr)
		}
		item, prepareErr := transactionPrepare(edit)
		if prepareErr != nil {
			abortErr, recovery := abortPrepared(prepared)
			return TransactionResult{RecoveryPaths: recovery}, errors.Join(prepareErr, abortErr)
		}
		prepared = append(prepared, preparedTransactionEdit{path: edit.Path, parent: parent, prepared: item})
		if prepareErr := revalidatePathIdentity(parent, "finish preparing transaction edit"); prepareErr != nil {
			abortErr, recovery := abortPrepared(prepared)
			return TransactionResult{RecoveryPaths: recovery}, errors.Join(prepareErr, abortErr)
		}
	}
	runTransactionHook(transactionAfterPrepareAll, "")

	applied := make([]AppliedEdit, 0, len(prepared))
	for i, item := range prepared {
		path := item.path
		runTransactionHook(transactionBeforeCommit, path)
		if boundaryErr := revalidatePathIdentity(item.parent, "commit transaction edit"); boundaryErr != nil {
			abortErr, abortRecovery := abortPrepared(prepared[i:])
			unresolved, rollbackRecovery, rollbackErr := rollbackApplied(applied, normalized.parents)
			return cloneTransactionResult(TransactionResult{Edits: unresolved, RecoveryPaths: mergeRecoveryPaths(abortRecovery, rollbackRecovery)}), errors.Join(boundaryErr, abortErr, rollbackErr)
		}
		edit, commitErr := item.prepared.Commit()
		if commitErr == nil {
			applied = append(applied, edit)
			continue
		}
		outcome, state := item.prepared.Outcome()
		if state == CommitVerifiedApplied || state == CommitUnverifiedAfterExchange {
			applied = append(applied, outcome)
		}
		abortErr, abortRecovery := abortPrepared(prepared[i+1:])
		unresolved, rollbackRecovery, rollbackErr := rollbackApplied(applied, normalized.parents)
		return cloneTransactionResult(TransactionResult{Edits: unresolved, RecoveryPaths: mergeRecoveryPaths(abortRecovery, rollbackRecovery)}), errors.Join(commitErr, abortErr, rollbackErr)
	}
	return cloneTransactionResult(TransactionResult{Edits: applied}), nil
}

func validateTransaction(proposal TransactionProposal) (validatedTransaction, error) {
	if !filepath.IsAbs(proposal.Repo) || !filepath.IsAbs(proposal.LockPath) {
		return validatedTransaction{}, errors.New("repository and lock paths must be absolute")
	}
	realRepo, err := filepath.EvalSymlinks(filepath.Clean(proposal.Repo))
	if err != nil {
		return validatedTransaction{}, fmt.Errorf("resolve repository path: %w", err)
	}
	wantLock, err := resolveRepoLock(realRepo)
	if err != nil {
		return validatedTransaction{}, err
	}
	lockParent, err := filepath.EvalSymlinks(filepath.Dir(filepath.Clean(proposal.LockPath)))
	if err != nil || filepath.Join(lockParent, filepath.Base(proposal.LockPath)) != wantLock.path {
		return validatedTransaction{}, errors.Join(errors.New("lock path is not the repository common Git lock"), err)
	}
	edits := make([]Proposal, 0, len(proposal.Edits))
	seen := make(map[string]struct{}, len(proposal.Edits))
	parents := make(map[string]pathIdentity, len(proposal.Edits))
	for _, edit := range proposal.Edits {
		if !Valid(edit) || !filepath.IsAbs(edit.Path) {
			return validatedTransaction{}, fmt.Errorf("invalid file proposal: %s", edit.Path)
		}
		parent, err := filepath.EvalSymlinks(filepath.Dir(filepath.Clean(edit.Path)))
		if err != nil {
			return validatedTransaction{}, fmt.Errorf("resolve proposal parent: %w", err)
		}
		parentInfo, err := os.Lstat(parent)
		if err != nil || !parentInfo.IsDir() {
			return validatedTransaction{}, errors.Join(fmt.Errorf("proposal parent must be a non-symlink directory: %s", parent), err)
		}
		normalizedPath := filepath.Join(parent, filepath.Base(edit.Path))
		relative, err := filepath.Rel(realRepo, normalizedPath)
		if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return validatedTransaction{}, errors.Join(fmt.Errorf("proposal target is outside repository: %s", edit.Path), err)
		}
		info, err := os.Lstat(normalizedPath)
		if err != nil || !info.Mode().IsRegular() {
			return validatedTransaction{}, errors.Join(fmt.Errorf("proposal target must be a regular non-symlink file: %s", edit.Path), err)
		}
		if _, duplicate := seen[normalizedPath]; duplicate {
			return validatedTransaction{}, fmt.Errorf("duplicate transaction target: %s", normalizedPath)
		}
		seen[normalizedPath] = struct{}{}
		copy := cloneProposal(edit)
		copy.Path = normalizedPath
		edits = append(edits, copy)
		parents[normalizedPath] = pathIdentity{path: parent, info: parentInfo}
	}
	sort.Slice(edits, func(i, j int) bool { return edits[i].Path < edits[j].Path })
	return validatedTransaction{proposal: TransactionProposal{Repo: realRepo, LockPath: wantLock.path, Edits: edits}, lock: wantLock, parents: parents}, nil
}

func rollbackApplied(applied []AppliedEdit, parents map[string]pathIdentity) ([]AppliedEdit, []string, error) {
	var unresolved []AppliedEdit
	var recovery []string
	var rollbackErr error
	for i := len(applied) - 1; i >= 0; i-- {
		edit := applied[i]
		runTransactionHook(transactionBeforeRollback, edit.Path)
		verifiedRolledBack, err := applyPreparedRevert(edit, parents[edit.Path])
		if err != nil {
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("rollback %s: %w", edit.Path, err))
		}
		if !verifiedRolledBack {
			unresolved = append(unresolved, cloneAppliedEdit(edit))
			recovery = append(recovery, edit.Path)
		}
	}
	sort.Slice(unresolved, func(i, j int) bool { return unresolved[i].Path < unresolved[j].Path })
	sort.Strings(recovery)
	return unresolved, recovery, rollbackErr
}

func applyPreparedRevert(edit AppliedEdit, parent pathIdentity) (bool, error) {
	if err := revalidatePathIdentity(parent, "propose transaction rollback"); err != nil {
		return false, err
	}
	proposal, err := ProposeRevert(edit)
	if err != nil {
		return false, err
	}
	if err := revalidatePathIdentity(parent, "prepare transaction rollback"); err != nil {
		return false, err
	}
	prepared, err := transactionPrepare(proposal)
	if err != nil {
		return false, err
	}
	if err := revalidatePathIdentity(parent, "finish preparing transaction rollback"); err != nil {
		return false, errors.Join(err, prepared.Abort())
	}
	if err := revalidatePathIdentity(parent, "commit transaction rollback"); err != nil {
		return false, errors.Join(err, prepared.Abort())
	}
	reversed, err := prepared.Commit()
	_, state := prepared.Outcome()
	return reversed.Path != "" || state == CommitVerifiedApplied, err
}

func abortPrepared(prepared []preparedTransactionEdit) (error, []string) {
	var err error
	var recovery []string
	for i := len(prepared) - 1; i >= 0; i-- {
		beforeErr := revalidatePathIdentity(prepared[i].parent, "abort prepared transaction edit")
		if abortErr := prepared[i].prepared.Abort(); abortErr != nil {
			err = errors.Join(err, fmt.Errorf("abort prepared edit %s: %w", prepared[i].path, abortErr))
			recovery = append(recovery, prepared[i].path)
		}
		afterErr := revalidatePathIdentity(prepared[i].parent, "finish aborting prepared transaction edit")
		if beforeErr != nil || afterErr != nil {
			err = errors.Join(err, beforeErr, afterErr)
			recovery = append(recovery, prepared[i].path)
		}
	}
	return err, mergeRecoveryPaths(recovery)
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
	validated, err := validateTransaction(TransactionProposal{Repo: repo, LockPath: lockPath, Edits: edits})
	if err != nil {
		return TransactionProposal{}, err
	}
	return validated.proposal, nil
}

func revalidatePathIdentity(identity pathIdentity, operation string) error {
	info, err := os.Lstat(identity.path)
	if err != nil || !info.IsDir() || identity.info == nil || !os.SameFile(info, identity.info) {
		return errors.Join(fmt.Errorf("%w: parent path identity changed before %s: %s", ErrStaleFile, operation, identity.path), err)
	}
	return nil
}

func mergeRecoveryPaths(groups ...[]string) []string {
	seen := make(map[string]struct{})
	for _, group := range groups {
		for _, path := range group {
			seen[path] = struct{}{}
		}
	}
	paths := make([]string, 0, len(seen))
	for path := range seen {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
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
