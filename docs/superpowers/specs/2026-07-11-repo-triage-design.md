# Repository Dirty-State Triage Design

Date: 2026-07-11
Status: Approved

## Goal

When the configured dotfiles repository has any dirty state, sys-bozo must show the exact affected entries and provide a Review-gated workflow to inspect and safely resolve them. A count without filenames is insufficient.

The flow is named **Repo Triage**. It supports tracked, staged, unstaged, renamed, deleted, conflicted, and untracked entries without inventing a one-click “clean everything” operation.

## Entry Point

The Home screen keeps the repository summary but makes dirty state actionable:

- `REPOSITORY  CLEAN` remains informational.
- `REPOSITORY  N DIRTY` is selectable and routes to `REPO/TRIAGE` with `Enter`.
- A status-probe failure renders `REPOSITORY  STATUS UNAVAILABLE`; it must never be represented as clean.

Repo Triage is also reachable from Inspect so users can refresh or review repository state without returning Home.

## Status Model

Git state is read with:

```text
git status --porcelain=v2 -z --untracked-files=all
```

NUL-delimited porcelain v2 records are parsed into structured entries. Display text is never reparsed into commands.

Each entry records:

- primary path and optional rename/copy source path;
- index status and worktree status independently;
- tracked, untracked, ignored, renamed/copied, deleted, or conflicted kind;
- mode/object metadata provided by porcelain v2;
- a stable display fingerprint used to preserve selection across refresh.

Paths may contain whitespace, Unicode, newlines, leading dashes, and other legal Git filename bytes. Internal APIs keep paths as exact strings from NUL records. Rendering sanitizes control characters without changing command arguments.

## Triage Interaction

The screen has `FILES` and `DIFF` tabs.

### Files

- `j`/`k` and `Down`/`Up` move identically.
- `Space` toggles multi-selection.
- `Enter` opens the focused entry's diff.
- `Tab` and `Shift+Tab` switch `FILES`/`DIFF`.
- Each row displays separate staged/index and unstaged/worktree states.
- Refresh preserves selection only when the entry's status fingerprint is unchanged.

### Diff

Tracked entries show staged and unstaged hunks separately:

- staged: `git diff --cached -- <path>`;
- unstaged: `git diff -- <path>`.

Untracked regular text files receive a bounded read-only preview. Binary, oversized, unreadable, deleted, directory, and symlink entries show explicit semantic states rather than fabricated text.

Diff content is viewport-backed and remains scrollable. Rendering must be usable at 80×24 and preserve meaning under `NO_COLOR`.

## Actions

The action bar offers only operations valid for the current selection.

### Commit

Commit is available for selected non-conflicted entries.

1. Collect a commit message in a focused text field.
2. Enter immutable Review showing paths and exact commands.
3. On confirmation, rerun status and reject stale fingerprints.
4. Run `git add -- <paths>`.
5. Run `git commit --only -m <message> -- <paths>` with native terminal handoff.

Terminal handoff is required because Git signing, hooks, credential helpers, or user configuration may prompt. sys-bozo must not capture those prompts or attempt credential priming.

`--only` is mandatory: unrelated paths that were already staged before Repo Triage must remain staged and must not enter this commit. If `git add` succeeds but commit fails, the refreshed Result truthfully shows the selected paths as staged; sys-bozo does not attempt a hidden index rollback.

### Stash

Stash is available for tracked or untracked selections. Review shows the exact path-scoped command:

```text
git stash push -u -m "sys-bozo repo triage" -- <paths>
```

`-u` is included only when the selection contains untracked entries. The stash is path-scoped; unrelated dirty entries remain untouched.

### Restore to HEAD

Restore is available only when every selected entry is tracked and non-conflicted. Review shows:

```text
git restore --source=HEAD --staged --worktree -- <paths>
```

This destructive action requires normal immutable Review confirmation. It is never mapped to a single shortcut from the file list.

### Delete Untracked

Untracked entries never use the generic Restore action. They receive a distinct `DELETE UNTRACKED` action.

1. Validate that every selected entry is still untracked and inside the repository.
2. Show the exact paths and dry-run result.
3. Require an explicit second confirmation labeled `DELETE UNTRACKED`.
4. Run `git clean -fd -- <paths>`.

Symlink targets are never followed. Tracked, ignored, conflicted, or status-changed paths abort the operation.

### Conflicts

Conflicted entries may be inspected or opened in `$EDITOR`. Commit, Restore, and Delete remain disabled while the selection contains a conflict. sys-bozo never chooses conflict sides or stages a conflict automatically.

## Review and Stale-State Contract

Every mutation is represented by an immutable reviewed operation containing:

- repository identity and working directory;
- selected structured entries and fingerprints;
- exact argument arrays;
- destructive/interactive classification;
- expected result and refresh behavior.

Review uses a stronger action fingerprint than the display fingerprint. For every selected path it includes the complete porcelain record, index object identity, and an exact worktree identity:

- regular files: SHA-256 of complete bytes plus mode;
- symlinks: SHA-256 of the link text plus mode, without following the target;
- missing/deleted paths: an explicit missing sentinel;
- directories: a typed directory sentinel; destructive untracked-directory deletion additionally revalidates every current descendant as untracked.

Immediately before execution, sys-bozo reruns porcelain status and recomputes every action fingerprint. Added, removed, renamed, restaged, content-changed, mode-changed, or otherwise changed entries make Review stale. Stale Review returns to triage with a warning and performs no mutation. Fingerprint collection is descriptor-based for regular files so metadata and bytes refer to one opened inode.

Commands are executed directly with argument arrays and `--` path boundaries. No filename or commit message is interpolated through a shell.

## Components

### Git status domain

A focused repository-status package owns:

- porcelain v2 `-z` parsing;
- status fingerprints;
- exact path handling;
- selection validation;
- bounded diff/preview loading;
- action proposal construction.

It depends on an injected command runner and filesystem reader so unit tests never touch the real repository.

### TUI state

Repo Triage stores:

- current status result or probe error;
- focused and selected entry fingerprints;
- FILES/DIFF tab;
- independent file cursor and diff viewport;
- selected action and commit message;
- delete-confirmation stage;
- immutable Review and final Result.

The TUI owns navigation and rendering. The Git domain owns parsing, validation, and exact commands.

### Refresh

Refresh runs asynchronously. The screen shows a truthful loading state and remains cancellable. Late results from superseded requests are ignored by request ID.

## Error and Output Handling

- Status errors render `STATUS UNAVAILABLE`, not clean state.
- Git stderr is bounded and control-sanitized before display/history.
- Raw diffs, commit messages, environment variables, credentials, signing output, and editor contents are not stored in history.
- Result/history record action, selected path count, status, duration, and a bounded provider-neutral summary.
- Partial Git failures trigger a fresh status read; the refreshed repository state is authoritative.
- Binary, oversized, unreadable, deleted, directory, and symlink previews have explicit states.

## Testing

All automated tests use temporary Git repositories and isolated local Git configuration. They must not read or mutate the user's real repo, global Git config, credentials, signing keys, or editor.

Required coverage:

- clean, modified, added, deleted, renamed, copied, untracked, ignored, staged+unstaged, and conflicted porcelain v2 records;
- filenames with spaces, Unicode, newlines, leading dashes, and rename source paths;
- malformed, truncated, unknown, and oversized porcelain records fail closed;
- status command failure becomes `STATUS UNAVAILABLE`;
- `j`/`k` and arrow keys produce identical movement;
- multi-select preserves exact paths and independent status fingerprints;
- staged/unstaged diff separation and bounded untracked preview states;
- no mutation before Review confirmation;
- stale status rejection before every action;
- exact `--`-bounded argument arrays for commit, stash, restore, and clean;
- commit `--only` excludes unrelated pre-staged paths and preserves them in the index;
- identical porcelain letters with changed worktree bytes fail the action-fingerprint stale check;
- commit terminal handoff and restoration;
- conflict selection disables unsafe actions;
- untracked deletion requires the second confirmation and never follows symlink targets;
- path-scoped stash does not affect unrelated dirt;
- refresh preserves only unchanged selections;
- partial failure refresh and truthful Result/history;
- 80×24 layout, scrollable diff, `NO_COLOR`, and long/control-bearing filenames;
- fake command/filesystem seams prove no real repository or package-manager mutation.

## Non-Goals

- Automatic conflict resolution.
- One-click discard of all repository changes.
- Branch creation, rebasing, pulling, pushing, or remote synchronization.
- Commit-message generation.
- Secret scanning or content classification of the user's changes.
- Replacing full Git clients such as lazygit; Repo Triage is a focused dirty-state workflow.
