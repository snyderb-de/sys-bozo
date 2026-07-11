package repostate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"
)

type PreviewKind uint8

const (
	PreviewDiff PreviewKind = iota
	PreviewText
	PreviewBinary
	PreviewOversized
	PreviewUnreadable
	PreviewMissing
	PreviewDirectory
	PreviewSymlink
)

type Preview struct {
	Staged   string
	Unstaged string
	Kind     PreviewKind
	Detail   string
}

func LoadPreview(ctx context.Context, runner Runner, filesystem FileSystem, repo, gitBin string, entry Entry) Preview {
	if entry.Index != StateUntracked && entry.Worktree != StateUntracked {
		staged, stagedErr := runner.Output(ctx, repo, gitBin, "diff", "--cached", "--", entry.Path)
		unstaged, unstagedErr := runner.Output(ctx, repo, gitBin, "diff", "--", entry.Path)
		preview := Preview{Kind: PreviewDiff, Staged: boundedDisplay(staged, 1<<20), Unstaged: boundedDisplay(unstaged, 1<<20)}
		if stagedErr != nil || unstagedErr != nil {
			preview.Kind = PreviewUnreadable
			preview.Detail = "diff unavailable"
		}
		return preview
	}

	name, err := containedPath(repo, entry.Path)
	if err != nil {
		return Preview{Kind: PreviewUnreadable, Detail: "unsafe path"}
	}
	info, err := filesystem.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return Preview{Kind: PreviewMissing, Detail: "file is missing"}
	}
	if err != nil {
		return Preview{Kind: PreviewUnreadable, Detail: "file is unreadable"}
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return Preview{Kind: PreviewSymlink, Detail: "symbolic link; target not followed"}
	}
	if info.IsDir() {
		return Preview{Kind: PreviewDirectory, Detail: "directory preview unavailable"}
	}
	if !info.Mode().IsRegular() {
		return Preview{Kind: PreviewUnreadable, Detail: "unsupported file type"}
	}
	if info.Size() > 256<<10 {
		return Preview{Kind: PreviewOversized, Detail: "file exceeds 256 KiB preview limit"}
	}
	handle, err := filesystem.OpenNoFollow(name)
	if err != nil {
		return Preview{Kind: PreviewUnreadable, Detail: "file is unreadable"}
	}
	defer handle.Close()
	data, err := io.ReadAll(io.LimitReader(handle, (256<<10)+1))
	if err != nil {
		return Preview{Kind: PreviewUnreadable, Detail: "file is unreadable"}
	}
	if len(data) > 256<<10 {
		return Preview{Kind: PreviewOversized, Detail: "file exceeds 256 KiB preview limit"}
	}
	if bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
		return Preview{Kind: PreviewBinary, Detail: "binary content"}
	}
	return Preview{Kind: PreviewText, Unstaged: sanitizeControls(string(data))}
}

func boundedDisplay(data []byte, limit int) string {
	truncated := len(data) > limit
	if truncated {
		data = data[:limit]
	}
	text := sanitizeControls(string(data))
	if truncated {
		text += "\n… preview truncated"
	}
	return text
}

func sanitizeControls(value string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t':
			return r
		case r < 0x20 || r == 0x7f:
			return '�'
		default:
			return r
		}
	}, value)
}

func (p Preview) String() string {
	if p.Detail != "" {
		return p.Detail
	}
	return fmt.Sprintf("staged:\n%s\nunstaged:\n%s", p.Staged, p.Unstaged)
}
