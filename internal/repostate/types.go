package repostate

import (
	"context"
	"io"
	"io/fs"
	"os"
)

type State byte

const (
	StateUnmodified State = '.'
	StateModified   State = 'M'
	StateAdded      State = 'A'
	StateDeleted    State = 'D'
	StateRenamed    State = 'R'
	StateCopied     State = 'C'
	StateUnmerged   State = 'U'
	StateUntracked  State = '?'
	StateIgnored    State = '!'
)

type Entry struct {
	Path               string
	OriginalPath       string
	Index              State
	Worktree           State
	Kind               byte
	Submodule          string
	HeadMode           string
	IndexMode          string
	WorktreeMode       string
	HeadObject         string
	IndexObject        string
	Score              string
	DisplayFingerprint [32]byte
}

type Status struct {
	Entries []Entry
	Err     error
}

type Runner interface {
	Output(context.Context, string, string, ...string) ([]byte, error)
}

type FileHandle interface {
	io.Reader
	Stat() (os.FileInfo, error)
	Close() error
}

type FileSystem interface {
	OpenNoFollow(string) (FileHandle, error)
	Lstat(string) (os.FileInfo, error)
	Readlink(string) (string, error)
	WalkDir(string, fs.WalkDirFunc) error
}
