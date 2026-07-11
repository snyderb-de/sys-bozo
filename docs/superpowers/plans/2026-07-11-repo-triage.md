# Repository Dirty-State Triage Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the dotfiles dirty count with an exact file list, staged/unstaged diff inspection, multi-selection, and Review-gated commit/stash/restore/delete actions.

**Architecture:** A pure `internal/repostate` domain parses NUL-delimited porcelain v2, fingerprints exact selected content, loads bounded previews, and constructs argument-array operations. Bubble Tea owns asynchronous refresh, FILES/DIFF navigation, selection, destructive confirmation, immutable Review, terminal handoff, and truthful Result/history.

**Tech Stack:** Go 1.24.2, Git porcelain v2 `-z`, SHA-256, Bubble Tea/Bubbles viewport + text input, existing runner/history/Review lifecycle, temporary Git repositories.

## Global Constraints

- Any dirty state must show exact entries; a probe failure renders `STATUS UNAVAILABLE`, never clean.
- Parse `git status --porcelain=v2 -z --untracked-files=all`; never parse display text into commands.
- Preserve exact path bytes internally, including spaces, Unicode, newlines, renames, and leading dashes.
- `j`/`k` and Up/Down must behave identically.
- Multi-selection uses exact status fingerprints and survives refresh only when unchanged.
- Review uses full action fingerprints including exact regular-file bytes or symlink text; unchanged status letters are insufficient.
- No mutation before immutable Review confirmation and immediate stale revalidation.
- Every command uses argument arrays and `--` path boundaries; no shell interpolation.
- Commit uses `git commit --only -m <message> -- <paths>` so unrelated staged paths never enter the commit.
- Commit uses native terminal handoff; prompts/signing/hooks remain outside the TUI.
- Restore is tracked/non-conflicted only.
- Untracked deletion is a separate action with a second explicit confirmation and never follows symlink targets.
- Conflicts are inspect/editor-only; sys-bozo never auto-resolves or stages them.
- Diffs, stderr, and history are bounded/sanitized; history excludes paths, diffs, commit messages, environment, and credentials.
- Tests use temporary repositories and isolated local Git config only; never touch the real repo or global Git config.
- Preserve 80x24 usability and `NO_COLOR` semantic labels.

## File Map

- `internal/repostate/types.go`: entries, status/result, fingerprints, previews, actions, operations.
- `internal/repostate/status.go`: porcelain v2 runner and NUL parser.
- `internal/repostate/fingerprint.go`: descriptor-backed action fingerprints and stale validation.
- `internal/repostate/preview.go`: staged/unstaged diffs and bounded untracked preview.
- `internal/repostate/action.go`: exact commit/stash/restore/clean operation proposals.
- `internal/repostate/*_test.go`: parser, path, fingerprint, preview, and operation fixtures.
- `internal/system/probe.go`: distinguish dirty count from status-unavailable.
- `internal/tui/repo_flow.go`: async status, selection, tabs, actions, stale validation, execution messages.
- `internal/tui/view_repo.go`: FILES/DIFF/Review/delete-confirm rendering.
- `internal/tui/model.go`, `view_home.go`, `view_inspect.go`, `view_plan.go`, `update.go`, `execution.go`: screen/routes/lifecycle integration.
- `internal/tui/model_test.go`: keyboard parity, no-write-before-review, terminal handoff, stale/delete/history, 80x24.
- `internal/history/history.go`: existing schema retained; action contains only operation kind and selected count.
- `README.md`, `TESTING.md`, `TODO.md`: user flow, safe fake-repo commands, roadmap state.

---

### Task 1: Parse Exact Git Status and Build Safe Fingerprints/Previews

**Files:**
- Create: `internal/repostate/types.go`
- Create: `internal/repostate/status.go`
- Create: `internal/repostate/status_test.go`
- Create: `internal/repostate/fingerprint.go`
- Create: `internal/repostate/fingerprint_test.go`
- Create: `internal/repostate/preview.go`
- Create: `internal/repostate/preview_test.go`
- Modify: `internal/system/probe.go`
- Modify: `internal/system/probe_test.go`

**Interfaces:**
- Produces: `Inspect`, `ParsePorcelainV2`, `FingerprintEntries`, `ValidateFingerprints`, and `LoadPreview`.
- Consumes: injected `Runner` and `FileSystem`; no Git mutation.
- Replaces: count-only `gitDirtyCount` behavior for the dotfiles repository.

- [ ] **Step 1: Write failing porcelain v2 parser tests**

```go
func TestParsePorcelainV2PreservesExactPathsAndStates(t *testing.T) {
	raw := strings.Join([]string{
		"1 .M N... 100644 100644 100644 abc def configs/a file.toml",
		"2 R. N... 100644 100644 100644 abc def R100 configs/new-name\nline.toml",
		"configs/old-name.toml",
		"? --leading-dash",
		"? 日本語.txt",
	}, "\x00") + "\x00"
	got, err := ParsePorcelainV2([]byte(raw))
	if err != nil { t.Fatal(err) }
	if len(got) != 4 { t.Fatalf("entries=%#v", got) }
	if got[0].Path != "configs/a file.toml" || got[0].Index != StateUnmodified || got[0].Worktree != StateModified { t.Fatalf("ordinary=%#v", got[0]) }
	if got[1].Path != "configs/new-name\nline.toml" || got[1].OriginalPath != "configs/old-name.toml" || got[1].Index != StateRenamed { t.Fatalf("rename=%#v", got[1]) }
	if got[2].Path != "--leading-dash" || got[3].Path != "日本語.txt" { t.Fatalf("paths=%#v", got) }
}
```

- [ ] **Step 2: Run parser tests and verify RED**

Run: `go test ./internal/repostate -run TestParsePorcelainV2 -count=1 -v`

Expected: compile failure because `internal/repostate` does not exist.

- [ ] **Step 3: Define exact status types and parser**

```go
type State byte
const (
	StateUnmodified State = '.'
	StateModified State = 'M'
	StateAdded State = 'A'
	StateDeleted State = 'D'
	StateRenamed State = 'R'
	StateCopied State = 'C'
	StateUnmerged State = 'U'
	StateUntracked State = '?'
	StateIgnored State = '!'
)

type Entry struct {
	Path, OriginalPath string
	Index, Worktree State
	Kind byte
	Submodule, HeadMode, IndexMode, WorktreeMode string
	HeadObject, IndexObject, Score string
	DisplayFingerprint [32]byte
}

type Status struct { Entries []Entry; Err error }

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

type ExecRunner struct{}
func (ExecRunner) Output(ctx context.Context, dir, name string, args ...string) ([]byte, error)

type RealFileSystem struct{}
func (RealFileSystem) OpenNoFollow(string) (FileHandle, error)

func Inspect(ctx context.Context, runner Runner, repo, gitBin string) Status
func ParsePorcelainV2([]byte) ([]Entry, error)
```

`Inspect` runs `git status --porcelain=v2 -z --untracked-files=all` with `Dir=repo`. Parse record types `1`, `2` plus its second NUL path, `u`, `?`, and `!`; reject unknown, missing fields, missing trailing NUL, more than 100,000 entries, or output above 32 MiB. Compute `DisplayFingerprint` from the complete raw record(s), not rendered text. Clone output/entries before returning.

- [ ] **Step 4: Write failing action-fingerprint stale tests**

```go
func TestValidateFingerprintsRejectsSameStatusAfterContentChange(t *testing.T) {
	repo := initTempRepo(t)
	path := filepath.Join(repo, "tracked.txt")
	os.WriteFile(path, []byte("first\n"), 0o600)
	entries := mustInspect(t, repo).Entries
	fingerprints, err := FingerprintEntries(context.Background(), ExecRunner{}, RealFileSystem{}, repo, "git", entries)
	if err != nil { t.Fatal(err) }
	os.WriteFile(path, []byte("other\n"), 0o600) // same M status and byte count
	if err := ValidateFingerprints(context.Background(), ExecRunner{}, RealFileSystem{}, repo, "git", fingerprints); !errors.Is(err, ErrStaleStatus) { t.Fatalf("err=%v", err) }
}
```

Use these isolated Git helpers:

```go
func initTempRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.name", "Fixture"}, {"config", "user.email", "fixture@example.invalid"}, {"config", "commit.gpgsign", "false"}} {
		cmd := exec.Command("git", args...); cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil { t.Fatalf("git %v: %v: %s", args, err, out) }
	}
	path := filepath.Join(repo, "tracked.txt")
	if err := os.WriteFile(path, []byte("base\n"), 0o600); err != nil { t.Fatal(err) }
	for _, args := range [][]string{{"add", "--", "tracked.txt"}, {"commit", "-qm", "fixture base"}} {
		cmd := exec.Command("git", args...); cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil { t.Fatalf("git %v: %v: %s", args, err, out) }
	}
	return repo
}

func mustInspect(t *testing.T, repo string) Status {
	t.Helper()
	status := Inspect(context.Background(), ExecRunner{}, repo, "git")
	if status.Err != nil { t.Fatal(status.Err) }
	return status
}
```

- [ ] **Step 5: Implement descriptor-backed action fingerprints**

```go
type ActionFingerprint struct {
	Path, OriginalPath string
	Status [32]byte
	Worktree [32]byte
	Mode os.FileMode
	Kind FingerprintKind
}

type FingerprintKind uint8
const (
	FingerprintRegular FingerprintKind = iota
	FingerprintSymlink
	FingerprintMissing
	FingerprintDirectory
)

func FingerprintEntries(context.Context, Runner, FileSystem, string, string, []Entry) ([]ActionFingerprint, error)
func ValidateFingerprints(context.Context, Runner, FileSystem, string, string, []ActionFingerprint) error
```

Require lexical and canonical containment inside `repo`. Regular files open with no-follow/nonblocking semantics, `Fstat`, read complete bytes through that descriptor, and re-`Fstat` before hashing. Symlinks use `Lstat` + `Readlink` and hash link text without following it. Missing and directory states use typed sentinels; untracked directories recursively inspect descendants and reject tracked/ignored/status-changed contents before delete proposals. Fingerprint computation reruns current porcelain status and requires the complete raw status fingerprint plus exact worktree identity.

- [ ] **Step 6: Add bounded preview and probe error distinction**

Define:

```go
type Preview struct { Staged, Unstaged string; Kind PreviewKind; Detail string }
func LoadPreview(ctx context.Context, runner Runner, fs FileSystem, repo, gitBin string, Entry) Preview
```

Tracked preview runs `git diff --cached -- <path>` and `git diff -- <path>`, each capped at 1 MiB. Untracked regular text reads at most 256 KiB; binary/NUL, oversized, unreadable, missing, directory, and symlink states return explicit kinds. Sanitize controls only for display copies.

Change system facts to carry `DotfilesStatusUnavailable bool`; Git failure sets it true and dirty count zero, while Home renders unavailable rather than clean. Keep working-directory `GitDirtyCount` compatibility.

- [ ] **Step 7: Run the task gate and commit**

Run: `gofmt -w internal/repostate internal/system && go test -race ./internal/repostate ./internal/system -count=1 && go test ./... -count=1`

Expected: PASS with temporary repos and no global config reads.

```bash
git add internal/repostate internal/system
git commit -m "feat(repo): inspect exact dirty state"
```

---

### Task 2: Add the Repo Triage FILES/DIFF Screen

**Files:**
- Create: `internal/tui/repo_flow.go`
- Create: `internal/tui/view_repo.go`
- Modify: `internal/tui/model.go`
- Modify: `internal/tui/view_home.go`
- Modify: `internal/tui/view_inspect.go`
- Modify: `internal/tui/update.go`
- Modify: `internal/tui/model_test.go`

**Interfaces:**
- Consumes: read-only `repostate.Inspect` and `LoadPreview` from Task 1.
- Produces: `screenRepoTriage`, async request messages, FILES/DIFF tabs, exact selection, and preview viewport.
- Performs: no Git mutation.

- [ ] **Step 1: Write failing route and keyboard-parity tests**

```go
func TestDirtyRepositoryRowOpensTriageAndShowsExactEntries(t *testing.T) {
	m := New()
	m.width, m.height = 80, 24
	m.facts.DotfilesDirty = 2
	m.repoStatus = repostate.Status{Entries: []repostate.Entry{{Path: "flake.nix", Worktree: repostate.StateModified}, {Path: "new file", Worktree: repostate.StateUntracked}}}
	m.homeRepoFocused = true
	next, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	got := next.(Model)
	if got.screen != screenRepoTriage { t.Fatalf("screen=%v", got.screen) }
	view := got.viewRepoTriage()
	for _, want := range []string{"REPO/TRIAGE", "flake.nix", "new file", "MODIFIED", "UNTRACKED"} {
		if !strings.Contains(view, want) { t.Fatalf("missing %q\n%s", want, view) }
	}
}

func TestRepoTriageArrowsMatchVimMovement(t *testing.T) {
	base := repoTriageFixture()
	vim, _ := base.handleRepoKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	arrow, _ := base.handleRepoKey(tea.KeyMsg{Type: tea.KeyDown})
	if vim.(Model).repoFlow.cursor != arrow.(Model).repoFlow.cursor { t.Fatalf("vim=%d arrow=%d", vim.(Model).repoFlow.cursor, arrow.(Model).repoFlow.cursor) }
}
```

Add this exact fixture beside the tests:

```go
func repoTriageFixture() Model {
	m := New()
	m.screen = screenRepoTriage
	m.width, m.height = 80, 24
	m.repoFlow = repoFlow{status: repostate.Status{Entries: []repostate.Entry{{Path: "one", Worktree: repostate.StateModified}, {Path: "two", Worktree: repostate.StateUntracked}}}, selected: map[[32]byte]bool{}}
	return m
}
```

- [ ] **Step 2: Run TUI tests and verify RED**

Run: `go test ./internal/tui -run 'TestDirtyRepositoryRow|TestRepoTriageArrows' -count=1 -v`

Expected: compile failure for Repo Triage state/routes.

- [ ] **Step 3: Add model state and asynchronous refresh**

```go
type repoTab uint8
const ( repoFiles repoTab = iota; repoDiff )

type repoFlow struct {
	status repostate.Status
	requestID uint64
	loading bool
	cursor int
	selected map[[32]byte]bool
	tab repoTab
	preview repostate.Preview
	diffVP viewport.Model
	notice string
}

type repoStatusMsg struct { requestID uint64; status repostate.Status }
type repoPreviewMsg struct { requestID uint64; fingerprint [32]byte; preview repostate.Preview }
```

Inject `inspectRepo` and `loadRepoPreview` functions in `Model`. Starting/refreshing increments request ID; stale messages are ignored. Refresh rebuilds selection only for fingerprints still present. Status error is retained and rendered; never synthesize empty clean state.

- [ ] **Step 4: Make the Home repository row actionable**

When dirty or unavailable, include the repository row in Home focus order before the three existing actions. Up/Down and `j`/`k` move over repository + actions; existing numeric `1`/`2`/`3` shortcuts still open Weekly/Add/Inspect. Enter on repository opens triage and starts refresh. Add `REPOSITORY  STATUS UNAVAILABLE` rendering from Task 1.

Add `REPOSITORY` as a fifth Inspect entry routing to the same screen.

- [ ] **Step 5: Implement FILES/DIFF interaction and rendering**

- `j`/Down and `k`/Up move identically.
- `Space` toggles the focused exact display fingerprint.
- `Enter` loads focused preview and opens DIFF.
- Tab/Shift+Tab switch FILES/DIFF.
- `r` refreshes; `Esc` returns Inspect/Home.
- FILES rows show index and worktree status separately plus selection marker.
- DIFF shows staged and unstaged headings or explicit preview state in a viewport.
- Control-bearing paths are display-sanitized/truncated only after the exact entry is selected.

At 80x24 show at most 12 file rows and a compact footer. `NO_COLOR` uses textual status labels.

- [ ] **Step 6: Add selection/preview/error/layout tests and commit**

Cover arrows/Vim, wraparound, multi-select, refresh preservation/removal, stale async result, status unavailable, staged+unstaged headings, binary/oversized/symlink states, scroll, exact paths, 80x24, and `NO_COLOR`.

Run: `NO_COLOR=1 go test -race ./internal/tui -run 'Test(Repo|DirtyRepository)' -count=1 && go test ./... -count=1`

```bash
git add internal/tui
git commit -m "feat(tui): inspect repository dirty state"
```

---

### Task 3: Propose and Execute Review-Gated Git Actions

**Files:**
- Create: `internal/repostate/action.go`
- Create: `internal/repostate/action_test.go`
- Modify: `internal/tui/repo_flow.go`
- Modify: `internal/tui/view_repo.go`
- Modify: `internal/tui/model.go`
- Modify: `internal/tui/view_plan.go`
- Modify: `internal/tui/update.go`
- Modify: `internal/tui/execution.go`
- Modify: `internal/tui/model_test.go`

**Interfaces:**
- Produces: `ProposeAction` and `ValidateOperation` with exact argument arrays.
- Consumes: selected entries and action fingerprints from Task 1.
- Integrates: existing Review/Running/Result lifecycle and terminal handoff.

- [ ] **Step 1: Write failing exact-operation tests**

```go
func TestProposeCommitUsesOnlyAndPathBoundary(t *testing.T) {
	entry := Entry{Path: "--odd name", Worktree: StateModified, DisplayFingerprint: sha256.Sum256([]byte("fixture status"))}
	fingerprint := ActionFingerprint{Path: entry.Path, Status: entry.DisplayFingerprint, Kind: FingerprintRegular, Worktree: sha256.Sum256([]byte("fixture bytes")), Mode: 0o600}
	op, err := ProposeAction(ActionRequest{Repo: "/repo", GitBin: "git", Kind: ActionCommit, Message: "fix config", Entries: []Entry{entry}, Fingerprints: []ActionFingerprint{fingerprint}})
	if err != nil { t.Fatal(err) }
	want := []Command{
		{Name: "git", Args: []string{"add", "--", "--odd name"}},
		{Name: "git", Args: []string{"commit", "--only", "-m", "fix config", "--", "--odd name"}, Interactive: true},
	}
	if !reflect.DeepEqual(op.Commands, want) { t.Fatalf("commands=%#v", op.Commands) }
}

func TestProposeDeleteUntrackedRequiresSecondConfirmation(t *testing.T) {
	entry := Entry{Path: "tmp dir", Worktree: StateUntracked, DisplayFingerprint: sha256.Sum256([]byte("untracked"))}
	_, err := ProposeAction(ActionRequest{Repo: "/repo", GitBin: "git", Kind: ActionDeleteUntracked, DeleteConfirmed: false, Entries: []Entry{entry}, Fingerprints: []ActionFingerprint{{Path: entry.Path, Status: entry.DisplayFingerprint, Kind: FingerprintDirectory}}})
	if !errors.Is(err, ErrDeleteConfirmationRequired) { t.Fatalf("err=%v", err) }
}
```

- [ ] **Step 2: Run action tests and verify RED**

Run: `go test ./internal/repostate -run TestPropose -count=1 -v`

Expected: compile failure for action types.

- [ ] **Step 3: Define pure action proposals**

```go
type ActionKind string
const (
	ActionCommit ActionKind = "commit"
	ActionStash ActionKind = "stash"
	ActionRestore ActionKind = "restore"
	ActionDeleteUntracked ActionKind = "delete-untracked"
)

type Command struct { Name string; Args []string; Interactive bool }
type ActionRequest struct {
	Repo, GitBin, Message string
	Kind ActionKind
	Entries []Entry
	Fingerprints []ActionFingerprint
	DeleteConfirmed bool
}
type Operation struct { Repo string; Kind ActionKind; Entries []Entry; Fingerprints []ActionFingerprint; Commands []Command; DryRun string }

func ProposeAction(ActionRequest) (Operation, error)
func ValidateOperation(context.Context, Runner, FileSystem, Operation) error
```

Sort cloned paths deterministically. Commit requires a nonblank single-line message up to 200 UTF-8 bytes, runs add then commit `--only`. Stash uses `push`, conditional `-u`, fixed message, then `--` paths. Restore requires all tracked/non-conflicted. Delete requires all untracked plus `DeleteConfirmed`, validates descendants, and proposes `git clean -fd -- <paths>` after a captured `git clean -nd -- <paths>` dry run. Reject mixed invalid selections and empty selection.

- [ ] **Step 4: Add action/commit-message/delete-confirm TUI state**

Use Bubbles text input for commit message. Visible action keys are `C COMMIT`, `S STASH`, `R RESTORE`, and `D DELETE UNTRACKED`; invalid actions are rendered disabled and keypresses do nothing. Delete enters a dedicated confirmation state showing exact paths + dry run. The user must type the exact literal `DELETE UNTRACKED` into a confirmation input and press Enter before Review can be built; any mismatch remains on confirmation with no mutation.

Extend `reviewedPlan` with `Repo *repoReview`; `repoReview` deep-clones `Operation`. `viewReview` shows operation, selected count, exact paths, exact commands, interactive TTY marker, destructive warning, and stale warning.

- [ ] **Step 5: Validate immediately before execution and map commands to WorkItems**

`confirmReviewedPlan` first runs `ValidateOperation` asynchronously. Stale response returns to Review/Triage with no command. Valid response calls `beginReviewedRun` and maps each command to `runner.WorkItem`, setting `ExecutionInteractive` only for commit. Never prime Git credentials or sudo.

On completion or partial failure, refresh status. History action is `repo:<kind>:<count>` only. Closing Result returns to Repo Triage with refreshed truth.

- [ ] **Step 6: Add no-mutation/stale/terminal/partial-failure tests**

Required tests:

- no runner call before Review confirmation;
- same status letters but changed bytes reject stale;
- unrelated staged file excluded from commit and remains staged in a temporary repo;
- stash affects only selected paths and uses `-u` only with untracked selection;
- restore rejected for untracked/conflicted selection;
- delete requires second confirmation, rejects changed/tracked/symlink-follow attempts;
- exact arguments preserve spaces/newlines/Unicode/leading dash;
- commit invokes injected terminal handoff;
- failed add does not invoke commit; failed commit refreshes selected paths as staged;
- history excludes path/message/diff content.

- [ ] **Step 7: Run task gate and commit**

Run: `go test -race ./internal/repostate ./internal/tui -run 'Test(Repo|Propose|Validate|DirtyRepository)' -count=1 && go test ./... -count=1 && go vet ./... && git diff --check`

```bash
git add internal/repostate internal/tui
git commit -m "feat(tui): resolve reviewed repository changes"
```

---

### Task 4: Complete Documentation and End-to-End Safety Gates

**Files:**
- Modify: `README.md`
- Modify: `TESTING.md`
- Modify: `TODO.md`
- Modify: `internal/repostate/*_test.go`
- Modify: `internal/tui/model_test.go`

**Interfaces:**
- Verifies the complete user story; no new production subsystem.
- Documents exact supported actions and explicit non-goals.

- [ ] **Step 1: Add a real temporary-repository workflow test**

Build a temp Git repo with isolated author config, an unrelated staged file, selected staged+unstaged file, rename, untracked file/directory, leading-dash path, symlink, binary, and merge conflict fixture. Drive status → FILES selection → DIFF → Review for each action through injected/real local Git as appropriate. Assert the real sys-bozo/dotfiles repos remain byte/status unchanged.

- [ ] **Step 2: Add responsive/NO_COLOR/PTY checks**

At 80x24 assert FILES, DIFF, delete-confirm, Review, Running, and Result fit and retain textual states. Under `NO_COLOR`, require no ANSI. Compile the TUI test binary and run the existing PTY smoke under `/usr/bin/script`; add a Git commit handoff child using a harmless fake Git executable and require TUI restoration markers.

- [ ] **Step 3: Update user and testing docs**

README explains dirty row routing, FILES/DIFF, Up/Down + `j`/`k`, Space multi-select, commit/stash/restore, second-confirm delete, conflicts, and stale Review. TESTING documents fake-repo commands and states that no global config/credentials/signing are used. TODO marks exact dirty-file display/triage complete without claiming branch/remote/conflict automation.

- [ ] **Step 4: Run the final feature gate**

```sh
gofmt -w internal/repostate internal/system internal/tui
go test -race ./... -count=1
go test ./... -count=1
go vet ./...
git diff --check
NO_COLOR=1 go test ./internal/tui -run 'Test(Repo|DirtyRepository|WorkflowViewsFit80x24)' -count=1 -v
nix build .#packages.$(nix eval --impure --raw --expr builtins.currentSystem).default
```

Run the compiled real-PTY smoke, the staged TruffleHog hook, and `git status --short`. Remove only Nix-generated `flake.lock` intent-to-add residue.

- [ ] **Step 5: Commit docs and final tests**

```bash
git add README.md TESTING.md TODO.md internal/repostate internal/tui
git commit -m "docs: document repository triage workflow"
```
