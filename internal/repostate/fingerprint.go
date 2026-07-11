package repostate

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/sys/unix"
)

var ErrStaleStatus = errors.New("repository state changed after review")

type FingerprintKind uint8

const (
	FingerprintRegular FingerprintKind = iota
	FingerprintSymlink
	FingerprintMissing
	FingerprintDirectory
)

type ActionFingerprint struct {
	Path         string
	OriginalPath string
	Status       [32]byte
	Worktree     [32]byte
	Mode         os.FileMode
	Kind         FingerprintKind
}

type RealFileSystem struct{}

func (RealFileSystem) OpenNoFollow(name string) (FileHandle, error) {
	fd, err := unix.Open(name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), name), nil
}

func (RealFileSystem) Lstat(name string) (os.FileInfo, error) { return os.Lstat(name) }
func (RealFileSystem) Readlink(name string) (string, error)   { return os.Readlink(name) }
func (RealFileSystem) WalkDir(root string, fn fs.WalkDirFunc) error {
	return filepath.WalkDir(root, fn)
}

func FingerprintEntries(ctx context.Context, runner Runner, filesystem FileSystem, repo, gitBin string, entries []Entry) ([]ActionFingerprint, error) {
	current := Inspect(ctx, runner, repo, gitBin)
	if current.Err != nil {
		return nil, current.Err
	}
	byPath := make(map[string]Entry, len(current.Entries))
	for _, entry := range current.Entries {
		byPath[entryKey(entry)] = entry
	}

	result := make([]ActionFingerprint, 0, len(entries))
	for _, requested := range entries {
		entry, ok := byPath[entryKey(requested)]
		if !ok || entry.DisplayFingerprint != requested.DisplayFingerprint {
			return nil, ErrStaleStatus
		}
		fullPath, err := containedPath(repo, entry.Path)
		if err != nil {
			return nil, err
		}
		kind, mode, identity, err := fingerprintPath(filesystem, repo, fullPath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) && (entry.Index == StateDeleted || entry.Worktree == StateDeleted) {
				kind, mode, identity = FingerprintMissing, 0, sha256.Sum256([]byte("missing"))
			} else {
				return nil, fmt.Errorf("fingerprint %q: %w", entry.Path, err)
			}
		}
		result = append(result, ActionFingerprint{
			Path: entry.Path, OriginalPath: entry.OriginalPath,
			Status: entry.DisplayFingerprint, Worktree: identity, Mode: mode, Kind: kind,
		})
	}
	return result, nil
}

func ValidateFingerprints(ctx context.Context, runner Runner, filesystem FileSystem, repo, gitBin string, expected []ActionFingerprint) error {
	status := Inspect(ctx, runner, repo, gitBin)
	if status.Err != nil {
		return status.Err
	}
	byPath := make(map[string]Entry, len(status.Entries))
	for _, entry := range status.Entries {
		byPath[entryKey(entry)] = entry
	}
	entries := make([]Entry, 0, len(expected))
	for _, fingerprint := range expected {
		entry, ok := byPath[fingerprint.Path+"\x00"+fingerprint.OriginalPath]
		if !ok || entry.DisplayFingerprint != fingerprint.Status {
			return ErrStaleStatus
		}
		entries = append(entries, entry)
	}
	actual, err := FingerprintEntries(ctx, runner, filesystem, repo, gitBin, entries)
	if err != nil {
		if errors.Is(err, ErrStaleStatus) {
			return ErrStaleStatus
		}
		return err
	}
	if len(actual) != len(expected) {
		return ErrStaleStatus
	}
	for i := range expected {
		if actual[i] != expected[i] {
			return ErrStaleStatus
		}
	}
	return nil
}

func entryKey(entry Entry) string { return entry.Path + "\x00" + entry.OriginalPath }

func containedPath(repo, name string) (string, error) {
	if name == "" || filepath.IsAbs(name) {
		return "", fmt.Errorf("unsafe repository path %q", name)
	}
	clean := filepath.Clean(name)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes repository: %q", name)
	}
	repoReal, err := filepath.EvalSymlinks(repo)
	if err != nil {
		return "", err
	}
	parentReal, err := filepath.EvalSymlinks(filepath.Join(repo, filepath.Dir(clean)))
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parentReal = filepath.Join(repoReal, filepath.Dir(clean))
	}
	rel, err := filepath.Rel(repoReal, parentReal)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("canonical path escapes repository: %q", name)
	}
	return filepath.Join(repo, clean), nil
}

func fingerprintPath(filesystem FileSystem, repo, name string) (FingerprintKind, os.FileMode, [32]byte, error) {
	info, err := filesystem.Lstat(name)
	if err != nil {
		return 0, 0, [32]byte{}, err
	}
	mode := info.Mode()
	if mode&os.ModeSymlink != 0 {
		target, err := filesystem.Readlink(name)
		if err != nil {
			return 0, 0, [32]byte{}, err
		}
		return FingerprintSymlink, mode, sha256.Sum256([]byte("symlink\x00" + target)), nil
	}
	if mode.IsDir() {
		hash, err := fingerprintDirectory(filesystem, repo, name)
		return FingerprintDirectory, mode, hash, err
	}
	if !mode.IsRegular() {
		return 0, mode, [32]byte{}, fmt.Errorf("unsupported file type %s", mode.Type())
	}
	handle, err := filesystem.OpenNoFollow(name)
	if err != nil {
		return 0, 0, [32]byte{}, err
	}
	defer handle.Close()
	before, err := handle.Stat()
	if err != nil || !before.Mode().IsRegular() {
		return 0, 0, [32]byte{}, fmt.Errorf("file changed while opening")
	}
	h := sha256.New()
	h.Write([]byte("regular\x00"))
	if _, err := io.Copy(h, handle); err != nil {
		return 0, 0, [32]byte{}, err
	}
	after, err := handle.Stat()
	if err != nil || before.Size() != after.Size() || before.ModTime() != after.ModTime() || before.Mode() != after.Mode() {
		return 0, 0, [32]byte{}, ErrStaleStatus
	}
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return FingerprintRegular, before.Mode(), sum, nil
}

func fingerprintDirectory(filesystem FileSystem, repo, root string) ([32]byte, error) {
	var names []string
	if err := filesystem.WalkDir(root, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if name != root {
			names = append(names, name)
		}
		return nil
	}); err != nil {
		return [32]byte{}, err
	}
	sort.Strings(names)
	h := sha256.New()
	h.Write([]byte("directory\x00"))
	for _, name := range names {
		rel, err := filepath.Rel(repo, name)
		if err != nil {
			return [32]byte{}, err
		}
		info, err := filesystem.Lstat(name)
		if err != nil {
			return [32]byte{}, err
		}
		fmt.Fprintf(h, "%s\x00%d\x00", rel, info.Mode())
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := filesystem.Readlink(name)
			if err != nil {
				return [32]byte{}, err
			}
			h.Write([]byte(target))
		} else if info.Mode().IsRegular() {
			_, _, sum, err := fingerprintPath(filesystem, repo, name)
			if err != nil {
				return [32]byte{}, err
			}
			h.Write(sum[:])
		} else if !info.IsDir() {
			return [32]byte{}, fmt.Errorf("unsupported directory entry %q", rel)
		}
	}
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum, nil
}
