package repostate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os/exec"
)

const (
	maxStatusBytes   = 32 << 20
	maxStatusEntries = 100_000
)

type ExecRunner struct{}

func (ExecRunner) Output(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return out, nil
}

func Inspect(ctx context.Context, runner Runner, repo, gitBin string) Status {
	out, err := runner.Output(ctx, repo, gitBin, "status", "--porcelain=v2", "-z", "--untracked-files=all")
	if err != nil {
		return Status{Err: err}
	}
	entries, err := ParsePorcelainV2(out)
	if err != nil {
		return Status{Err: err}
	}
	return Status{Entries: append([]Entry(nil), entries...)}
}

func ParsePorcelainV2(raw []byte) ([]Entry, error) {
	if len(raw) > maxStatusBytes {
		return nil, fmt.Errorf("git status output exceeds %d bytes", maxStatusBytes)
	}
	if len(raw) == 0 {
		return []Entry{}, nil
	}
	if raw[len(raw)-1] != 0 {
		return nil, fmt.Errorf("git status output is not NUL terminated")
	}

	records := bytes.Split(raw[:len(raw)-1], []byte{0})
	entries := make([]Entry, 0, len(records))
	for i := 0; i < len(records); i++ {
		if len(entries) >= maxStatusEntries {
			return nil, fmt.Errorf("git status contains more than %d entries", maxStatusEntries)
		}
		record := records[i]
		if len(record) < 2 || record[1] != ' ' {
			return nil, fmt.Errorf("malformed porcelain record")
		}
		var entry Entry
		entry.Kind = record[0]
		switch record[0] {
		case '1':
			fields := bytes.SplitN(record, []byte{' '}, 9)
			if len(fields) != 9 || len(fields[1]) != 2 {
				return nil, fmt.Errorf("malformed ordinary porcelain record")
			}
			entry.Index, entry.Worktree = State(fields[1][0]), State(fields[1][1])
			entry.Submodule = string(fields[2])
			entry.HeadMode, entry.IndexMode, entry.WorktreeMode = string(fields[3]), string(fields[4]), string(fields[5])
			entry.HeadObject, entry.IndexObject = string(fields[6]), string(fields[7])
			entry.Path = string(fields[8])
			entry.DisplayFingerprint = sha256.Sum256(record)
		case '2':
			fields := bytes.SplitN(record, []byte{' '}, 10)
			if len(fields) != 10 || len(fields[1]) != 2 || i+1 >= len(records) {
				return nil, fmt.Errorf("malformed rename/copy porcelain record")
			}
			entry.Index, entry.Worktree = State(fields[1][0]), State(fields[1][1])
			entry.Submodule = string(fields[2])
			entry.HeadMode, entry.IndexMode, entry.WorktreeMode = string(fields[3]), string(fields[4]), string(fields[5])
			entry.HeadObject, entry.IndexObject = string(fields[6]), string(fields[7])
			entry.Score, entry.Path = string(fields[8]), string(fields[9])
			i++
			entry.OriginalPath = string(records[i])
			joined := make([]byte, 0, len(record)+1+len(records[i]))
			joined = append(joined, record...)
			joined = append(joined, 0)
			joined = append(joined, records[i]...)
			entry.DisplayFingerprint = sha256.Sum256(joined)
		case 'u':
			fields := bytes.SplitN(record, []byte{' '}, 11)
			if len(fields) != 11 || len(fields[1]) != 2 {
				return nil, fmt.Errorf("malformed unmerged porcelain record")
			}
			entry.Index, entry.Worktree = State(fields[1][0]), State(fields[1][1])
			entry.Submodule = string(fields[2])
			entry.HeadMode, entry.IndexMode, entry.WorktreeMode = string(fields[3]), string(fields[4]), string(fields[6])
			entry.HeadObject, entry.IndexObject = string(fields[7]), string(fields[8])
			entry.Path = string(fields[10])
			entry.DisplayFingerprint = sha256.Sum256(record)
		case '?', '!':
			entry.Path = string(record[2:])
			entry.Index, entry.Worktree = State(record[0]), State(record[0])
			entry.DisplayFingerprint = sha256.Sum256(record)
		default:
			return nil, fmt.Errorf("unknown porcelain record type %q", record[0])
		}
		if entry.Path == "" {
			return nil, fmt.Errorf("porcelain record has empty path")
		}
		entries = append(entries, entry)
	}
	return entries, nil
}
