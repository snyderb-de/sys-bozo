package repostate

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

var (
	ErrEmptySelection             = errors.New("select at least one repository entry")
	ErrInvalidSelection           = errors.New("selection is not valid for this action")
	ErrInvalidCommitMessage       = errors.New("commit message must be one nonblank line of at most 200 UTF-8 bytes")
	ErrDeleteConfirmationRequired = errors.New("delete requires the exact second confirmation")
)

type ActionKind string

const (
	ActionCommit          ActionKind = "commit"
	ActionStash           ActionKind = "stash"
	ActionRestore         ActionKind = "restore"
	ActionDeleteUntracked ActionKind = "delete-untracked"
)

type Command struct {
	Name        string
	Args        []string
	Interactive bool
}

type ActionRequest struct {
	Repo            string
	GitBin          string
	Message         string
	Kind            ActionKind
	Entries         []Entry
	Fingerprints    []ActionFingerprint
	DeleteConfirmed bool
}

type Operation struct {
	Repo         string
	GitBin       string
	Kind         ActionKind
	Entries      []Entry
	Fingerprints []ActionFingerprint
	Commands     []Command
	DryRun       string
}

func ProposeAction(request ActionRequest) (Operation, error) {
	if len(request.Entries) == 0 || len(request.Entries) != len(request.Fingerprints) {
		return Operation{}, ErrEmptySelection
	}
	if request.GitBin == "" {
		request.GitBin = "git"
	}

	type selected struct {
		entry       Entry
		fingerprint ActionFingerprint
	}
	items := make([]selected, len(request.Entries))
	for i := range request.Entries {
		if request.Entries[i].Path == "" || request.Entries[i].Path != request.Fingerprints[i].Path || request.Entries[i].DisplayFingerprint != request.Fingerprints[i].Status {
			return Operation{}, ErrInvalidSelection
		}
		items[i] = selected{entry: request.Entries[i], fingerprint: request.Fingerprints[i]}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].entry.Path < items[j].entry.Path })

	entries := make([]Entry, len(items))
	fingerprints := make([]ActionFingerprint, len(items))
	var paths []string
	for i, item := range items {
		entries[i] = item.entry
		fingerprints[i] = item.fingerprint
		paths = append(paths, item.entry.Path)
		if item.entry.OriginalPath != "" {
			paths = append(paths, item.entry.OriginalPath)
		}
	}
	sort.Strings(paths)
	paths = compactStrings(paths)
	op := Operation{Repo: request.Repo, GitBin: request.GitBin, Kind: request.Kind, Entries: entries, Fingerprints: fingerprints}

	withPaths := func(prefix ...string) []string {
		args := append([]string(nil), prefix...)
		args = append(args, "--")
		return append(args, paths...)
	}
	switch request.Kind {
	case ActionCommit:
		message := strings.TrimSpace(request.Message)
		if message == "" || message != request.Message || strings.ContainsAny(message, "\r\n") || len(message) > 200 || !utf8.ValidString(message) {
			return Operation{}, ErrInvalidCommitMessage
		}
		if containsConflict(entries) {
			return Operation{}, ErrInvalidSelection
		}
		op.Commands = []Command{
			{Name: request.GitBin, Args: withPaths("add")},
			{Name: request.GitBin, Args: withPaths("commit", "--only", "-m", message), Interactive: true},
		}
	case ActionStash:
		if containsConflict(entries) {
			return Operation{}, ErrInvalidSelection
		}
		args := []string{"stash", "push"}
		if containsUntracked(entries) {
			args = append(args, "-u")
		}
		args = append(args, "-m", "sys-bozo reviewed stash")
		op.Commands = []Command{{Name: request.GitBin, Args: withPaths(args...)}}
	case ActionRestore:
		if containsConflict(entries) || containsUntracked(entries) {
			return Operation{}, ErrInvalidSelection
		}
		op.Commands = []Command{{Name: request.GitBin, Args: withPaths("restore", "--source=HEAD", "--staged", "--worktree")}}
	case ActionDeleteUntracked:
		if !request.DeleteConfirmed {
			return Operation{}, ErrDeleteConfirmationRequired
		}
		if !allUntracked(entries) {
			return Operation{}, ErrInvalidSelection
		}
		dryArgs := withPaths("clean", "-nd")
		op.DryRun = commandDisplay(request.GitBin, dryArgs)
		op.Commands = []Command{{Name: request.GitBin, Args: withPaths("clean", "-fd")}}
	default:
		return Operation{}, fmt.Errorf("unknown repository action %q", request.Kind)
	}
	return cloneOperation(op), nil
}

func compactStrings(values []string) []string {
	if len(values) < 2 {
		return values
	}
	result := values[:1]
	for _, value := range values[1:] {
		if value != result[len(result)-1] {
			result = append(result, value)
		}
	}
	return result
}

func ValidateOperation(ctx context.Context, runner Runner, filesystem FileSystem, operation Operation) error {
	if len(operation.Entries) == 0 || len(operation.Entries) != len(operation.Fingerprints) {
		return ErrInvalidSelection
	}
	gitBin := operation.GitBin
	if gitBin == "" {
		gitBin = "git"
	}
	return ValidateFingerprints(ctx, runner, filesystem, operation.Repo, gitBin, operation.Fingerprints)
}

func cloneOperation(operation Operation) Operation {
	cloned := operation
	cloned.Entries = append([]Entry(nil), operation.Entries...)
	cloned.Fingerprints = append([]ActionFingerprint(nil), operation.Fingerprints...)
	cloned.Commands = make([]Command, len(operation.Commands))
	for i, command := range operation.Commands {
		cloned.Commands[i] = command
		cloned.Commands[i].Args = append([]string(nil), command.Args...)
	}
	return cloned
}

func containsConflict(entries []Entry) bool {
	for _, entry := range entries {
		if entry.Kind == 'u' || entry.Index == StateUnmerged || entry.Worktree == StateUnmerged {
			return true
		}
	}
	return false
}

func containsUntracked(entries []Entry) bool {
	for _, entry := range entries {
		if entry.Index == StateUntracked || entry.Worktree == StateUntracked {
			return true
		}
	}
	return false
}

func allUntracked(entries []Entry) bool {
	for _, entry := range entries {
		if entry.Index != StateUntracked && entry.Worktree != StateUntracked {
			return false
		}
	}
	return true
}

func commandDisplay(name string, args []string) string {
	parts := append([]string{name}, args...)
	for i, part := range parts {
		if strings.ContainsAny(part, " \t\r\n") {
			parts[i] = fmt.Sprintf("%q", part)
		}
	}
	return strings.Join(parts, " ")
}
