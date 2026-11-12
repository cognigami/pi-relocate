# relo

Move one independent checkout and its default-storage Pi conversations without
forking sessions or changing version-control history. Go standard library only.
The command is `relo`; this repository remains `pi-relocate`.

```text
relo [--dry-run] SOURCE DESTINATION
relo --help
```

Relative paths are relative to the caller. The destination is the exact new
checkout path, must not exist, and is never merged or overwritten. Missing
parents are created. Paths with spaces are supported; quote them in your shell.
Exit status is 0 for a validated preview or completed durable relocation, 1 for
usage errors, refusals, failures, or failed verification.

## Quick start

1. Stop processes and Pi sessions using the checkout or affected session files.
2. Make a separate backup of `~/.pi/agent/sessions`.
3. From **outside** the source checkout, preview: `relo --dry-run SOURCE DESTINATION`.
4. Review the preview, then execute: `relo SOURCE DESTINATION`.
   If execution fails, keep all copies and inspect the recovery instructions;
   do not blindly rerun.
5. After the last move, rebuild the global index with `/session-index` in Pi
   (any checkout using the same default storage). In each destination, use
   `pi -r` to resume an existing conversation and confirm its working directory.
6. Adjust reported external configuration separately. Keep backups and printed
   recovery copies until the result is accepted.

For the family layout below, move `perry` to `perry/root` **before** moving
Engine1 to `perry/engine1`. Build with `just build`; the executable is `bin/relo`.

## Prerequisites and supported scope

- Stop affected Pi sessions and all other processes using the checkout or its
  session files. Run from **outside the source checkout**. There is no process
  detection, live-session migration, or confirmation prompt.
- macOS/Linux, Go 1.25+ to build, Git on PATH. Validated with Git 2.55.0 and
  JJ 0.43.0 on macOS. JJ support is intentionally pinned to the proven version.
- An independent `.git` directory, optionally with independent colocated `.jj`
  metadata, one JJ workspace, and `.jj/repo/store/git_target` exactly
  `../../../.git`. Linked Git worktrees, submodules, external object stores,
  linked JJ repositories/additional workspaces, nested VC checkouts, and
  location-dependent Git registration/configuration are refused. No VC repair or conversion occurs.
- Same-filesystem directory renames only. Device IDs are checked before writes,
  including the sibling staging path for a container move. Nested mounts,
  symlinked path components and noncanonical case spellings are refused, not
  silently resolved. Symlinks inside ordinary checkout content move unchanged;
  symlinks inside VC metadata or session storage are unsupported. Special files
  such as sockets inside a checkout are refused.
- Pi's default `~/.pi/agent/sessions/<encoded-cwd>` storage. Nonempty
  `PI_CODING_AGENT_DIR` / `PI_CODING_AGENT_SESSION_DIR` or `sessionDir` settings in
  global, source-project, or caller-project settings are refused. The command
  has no `--session-dir` option. Past CLI-only customizations cannot be inferred:
  the operator must establish that the affected sessions use default storage.
- Compatibility is based on installed Pi 1.5.2, core session headers/tree JSONL,
  Toolbox sidecars, and the scoped `pi-sessions` 0.23.1 operational formats in
  [`docs/CONTRACT.md`](docs/CONTRACT.md). The tests also exercise legacy optional
  fields without schema normalization. Other formats are not claimed supported.

## Preview and execution

```sh
# Example only: separately authorize each real move after reviewing its preview.
cd ~/Documents
relo --dry-run perry perry/root
relo perry perry/root
relo --dry-run engine1 perry/engine1
relo engine1 perry/engine1
```

The first operation stages the checkout as an unused sibling, creates `perry` as
an empty container, then renames the checkout to `perry/root`. The second moves
only Engine1. Only a **direct child** destination inside the source is supported;
arbitrarily deep self-nesting is refused. Ordinary destinations may have missing
parents.

Preview and execution use the same freshly computed plan. A preview writes no
staging, backups, index, configuration, or checkout data. It reports checkout and
session locations, session/sidecar/orphan counts, operational field updates,
affected external session files, and configuration warnings. Its inventory is
not an authorization token or saved plan.

The entire checkout moves, including dirty, untracked, and ignored files. The
source session bucket moves in full, including unchanged `.sidecar.json` files
and orphan sidecars. A mixed-owner bucket, session ID collision, existing target
bucket (even empty), unknown bucket file, ambiguous metadata, or hard-linked
session requiring rewrite is refused before writes. Nested-directory and sibling
sessions do not move implicitly.

Only these operational fields change, throughout all conversation branches and
in affected external sessions:

- Core header `cwd` for moving sessions; exact mapped `parentSession` targets.
- `pi-sessions.subagent_launched` custom entry `data.childSessionFile` targets and
  exact moved `data.cwd` values.
- `pi-sessions.handoff-bootstrap` custom entry `data.parentSessionFile` targets.

IDs, writer/owner identities, names, timestamps, tree links, unknown fields,
numeric literals, historical messages, tool receipts, reports, endpoint snapshots,
and arbitrary old-path strings remain unchanged. Untouched lines retain their
exact bytes and line endings. Changed JSON objects may have different whitespace
and key ordering. File modes and modification times are preserved; file-system
birth/access times, inode identity, ACLs and extended attributes of **rewritten
session files** are not compatibility guarantees. Sidecars are not rewritten.

Known unsupported operational dependencies are reported and refused when their
paths would dangle: `pi-sessions.lineage` file references, non-subagent handoff
receipts used by the handoff board, and path-dependent subagent resume shell
commands. These are not rewritten by guessing at arbitrary command strings.
Ordinary historical subagent receipts remain untouched. Unknown historical custom
data is not automatically treated as operational metadata.

## Verification and manual finishing

Execution saves and checks originals of every session it will rewrite, including
external sessions, before moving the checkout. It prints retained recovery and
staging locations before the first rename. Each transformed session is prepared
beside its target and published by rename.

Durable verification compares all checkout file bytes, modes and mtimes,
including raw `.git`/`.jj` metadata, checks Git/JJ usability without snapshotting,
compares transformed sessions against the plan, checks ownership and encoded
storage, verifies sidecar/orphan bytes, and checks session modes and recency
modification times. This does **not** rebuild or claim verification of the derived
extension index or live extension behavior.

The final output on actual success is:

```text
Reminder: start Pi in <destination> and use /session-index to rebuild the session index.
```

Rebuild manually in interactive Pi; one rebuild may follow several completed
moves. Then check `pi -r`, resume existing conversations, and verify working
directory, session names, navigation/bookmarks, and parent/child relationships.
Keep recovery copies until the operator accepts the result.

The index database, messaging broker/sockets, external configuration and the
`pi-sessions` package are not modified. Project-local absolute paths in virtual
environments, build artifacts and other integrations are outside scope.

### External configuration

When the known `~/.config/pi-extensions/sandbox/99-mini.jsonc` refers to the source,
the preview reports it for **separate manual adjustment**. Update the local
project selector and local staging path. A selector matching the old family root
and all descendants must not unintentionally select sibling checkouts. Do not
change the remote working directory merely because the local path moved.
Other external path-dependent configuration is not exhaustively discoverable.

## Failure and recovery

This is **not a group-atomic transaction**. Do not blindly rerun a partially
completed command. Normal failures report completed steps and current checkout,
session-collection and recovery locations. A process kill or power loss cannot
promise an error message or automatic rollback.

Backups are retained under `~/.pi/agent/relo-recovery-*`, outside the checkout and
session buckets. `RECOVERY.md` is a static original-to-target file map, not a
transaction journal. Originals are numbered `.jsonl` files with original modes
and modification times. A failed backup can leave an incomplete backup set, but
the checkout and original session collection have not yet moved.

With affected processes stopped:

1. Read the printed locations and `RECOVERY.md`. Inspect which checkout and
   session bucket actually exist. A container move can leave the checkout at its
   printed sibling `.relo-*` path, with the old path absent or an empty container.
2. Preserve all surviving data. If undoing the move, rename the session bucket
   back when needed, then restore backed-up session files to their mapped
   original paths, including external inbound-reference files. Preserve their
   original modes and mtimes when copying. A partial publication may leave a
   mixture of original and rewritten files; use the map, not an assumption that
   every file was updated.
3. Rename the checkout back. For a completed container move, first stage the
   checkout as a sibling, remove only the **empty** container, and rename the
   staged checkout to the original source. Never overwrite an existing checkout.
4. Inspect any retained `<session-target>.relo-*` replacement files. They are not
   the original backups. Do not delete the only surviving copy of any data.
5. Verify recovery before planning another relocation. If retaining the move
   instead, finish metadata/configuration deliberately from the saved map and
   verify the result before rebuilding the index. There is no resume command or
   automatic cleanup service. Recovery-copy deletion is an operator decision.

## Development and fixture evidence

Pi's `build`, `lint`, and `test` tools discover these canonical Justfile recipes:

```sh
just build                 # bin/relo
just lint                  # gofmt + go vet
just test                  # complete Go suite
just test internal/relocate # focused package
```

No third-party Go dependencies. Generated binaries and repository-local
`.fixtures/` directories are ignored. Tests use synthetic conversations and
isolated homes; they never copy real conversations or relocate real projects.
Disposable fixture initialization uses Git/JJ subprocesses within the Go tests,
run through Pi's test tool, not interactive project commands. The command tests
also check installed Pi's session lookup against an isolated destination bucket
when the installed Pi/Node runtime is available (otherwise explicitly skipped).
See [`docs/EXECUTION-PLAN.md`](docs/EXECUTION-PLAN.md) for gates and evidence.
Real-project use and interactive acceptance require separate operator approval.
