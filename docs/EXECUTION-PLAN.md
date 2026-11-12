# relo execution plan

Status: implementation authorized by the operator; real-project relocation is
not authorized. Implementation steps 1–6 are complete and fixture-validated,
subject to the explicit validation limit below. Step 7 remains operator-controlled.

Sources of truth: `CONTRACT.md` and `CLI-DESIGN.md`. Implement in Go, with the
standard library unless an established requirement proves it insufficient.
The executable is `relo`; the repository can remain named `pi-relocate`.

## 1. Establish the checkout-move mechanism

Before building the mutator, resolve the remaining location-sensitive VC detail
using installed documentation/source and disposable repositories:

- Confirm how an independent Git clone with colocated JJ metadata can be moved
  without changing history or working-copy state.
- Exercise the actual required layout: independent `.git` and `.jj` directories,
  with JJ's Git target relative to the checkout.
- Determine whether JJ records a working-directory location that needs a supported
  update. Do not assume a directory rename is sufficient merely because Git
  moves cleanly.
- Prove both a normal sibling move and `perry -> perry/root`.
- Compare history references, bookmarks, dirty/untracked/ignored files, and
  working-copy state before and after. Avoid inspection commands that themselves
  snapshot or rewrite the state being compared.

Prefer same-filesystem renames for the first implementation. Establish any
same-filesystem limit before coding mutation: unsupported cross-device moves
must be refused before mutation, not discovered after partly moving data.
Likewise establish how ambiguous symlink/aliased paths are refused or resolved.
These are explicit first-version limits, not silent copy/delete fallbacks.

**Gate: passed on disposable fixtures.** `internal/moveprobe/move_test.go`, run
through Pi's `test` tool, proves sibling rename and `perry -> perry/root` using
Git 2.55.0 and JJ 0.43.0. Fixtures are independent Git clones initialized with
`jj git init --colocate`; `.jj/repo/store/git_target` is `../../../.git`.
Both moves preserve every checkout file's bytes, mode, and mtime, including raw
`.git` and `.jj` operation/working-copy metadata, dirty tracked files, untracked
files, and ignored files. Readback of Git refs/HEAD and JJ history, bookmarks,
workspace list, and root uses non-snapshotting commands (`--ignore-working-copy`
for JJ). These reads are also checked not to change the raw metadata. JJ root
follows the rename without updating a stored working-directory path.

Proven sequence: rename directly for a sibling destination; for a direct child,
rename the checkout to an unused sibling, create its old path as a container,
then rename the staged checkout into that container. No VC repair is needed.
The first version supports only independent Git clones, optionally with the
proven colocated JJ layout and a single workspace. Refuse linked worktrees,
external object stores, linked JJ repos, additional workspaces, symlink/aliased
path components, nested mounts, and different source/destination devices before
mutation. Compare filesystem device IDs at the nearest existing parents; do
not fall back to copy/delete. Only a direct child inside the source is supported.

## 2. Bootstrap the Go command and fixture environment

Create a small Go module, a `cmd/relo` entry point, and one internal relocation
package. Split source files by responsibility as needed; do not build a general
filesystem, plugin, or transaction framework.

Expected initial file layout:

```text
cmd/relo/main.go
internal/relocate/       # Planning, session edits, execution, verification
internal/relocate/*_test.go
internal/relocate/testdata/  # Small synthetic session examples if useful
go.mod
Justfile
README.md
.gitignore
```

- Implement exactly `relo [--dry-run] SOURCE DESTINATION` and `relo --help`.
- Keep argument parsing and terminal output separate from relocation logic so
  the command can be tested without touching a user's checkout or Pi storage.
- Pass fixture storage locations internally in tests; do not add product CLI
  flags for test configuration.
- Expose canonical `build`, `lint`, and `test` Justfile recipes for Pi's development
  tools. Build `relo`; let lint own Go formatting and static checks.
- Keep generated binaries and disposable test directories out of source control.
- Use synthetic, repository-local disposable fixtures, not copied conversations
  or the real Perry/Engine1 directories.

**Gate:** the command builds, help/usage/exit behavior is tested, and the standard
build/lint/test tool surfaces work. No relocation implementation is required yet.

## 3. Implement read-only discovery and dry-run

Build an in-memory operation plan fresh for each invocation:

1. Resolve source and destination paths and reject invalid relationships,
   existing destinations, unsupported checkout arrangements, and a caller inside
   the source checkout.
2. Resolve default Pi storage using the installed Pi path-encoding convention.
   Do not silently operate on default storage if an explicit unsupported storage
   customization is detected.
3. Inventory the source session bucket and all sidecars, including orphans.
   Check header ownership against the exact source working directory. Refuse an
   ambiguous mixed-owner bucket rather than moving unrelated sessions or
   guessing ownership of orphan files.
4. Construct an exact old-session-file to new-session-file mapping. Use this
   mapping, not substring replacement, for session-file references.
5. Scan default-storage sessions for recognized operational inbound references,
   including references from sessions outside the source bucket. Report the
   affected external files in the preview.
6. Validate the affected records and refuse collisions or metadata that cannot
   safely be understood. Unknown historical custom data is not automatically an
   error or an instruction to rewrite it.
7. Report known external configuration adjustments, including Perry's `mini`
   overlay. Read/report only; do not modify configuration or remote paths.

Share this planner between dry-run and execution. Dry-run creates no staging,
backups, recovery directories, index changes, or configuration changes.

**Gate:** dry-run accurately reports a fixture containing Perry, Engine1, and an
external DTCC parent reference, with a before/after filesystem comparison showing
no changes. Include refusals for destination and session collisions, misleading
path prefixes, unsupported layouts, and malformed affected metadata.

## 4. Implement narrow session transformations

Create and test the transformations independently of directory moves:

- Rewrite moved sessions' header `cwd` and mapped `parentSession` targets.
- Rewrite mapped `pi-sessions.subagent_launched.data.childSessionFile` targets
  and exact moved `data.cwd` values.
- Rewrite mapped `pi-sessions.handoff-bootstrap.data.parentSessionFile` targets.
- Apply these edits to every tree branch and recognized external inbound record.
- Preserve ownership IDs and legacy field spellings; do not normalize old
  records to the latest schema.
- Leave unrelated outbound children, historical message endpoints, subagent tool
  receipts, reports, stored research data, and arbitrary strings unchanged.
- Move sidecars without changing their bytes.

Stream JSONL without Go Scanner's default small-line limit. Preserve untouched
lines byte-for-byte and preserve all non-location values in rewritten records,
including large numeric values and unknown fields. Do not parse arbitrary JSON
numbers through float64 and accidentally change them.

**Gate:** tests cover branching trees, legacy launch/bootstrap records, inbound
DTCC references, unrelated children, long JSONL records, trailing-newline handling,
large numeric values, sidecars/orphans, and old paths inside historical content.
Only permitted location fields differ in transformed records.

## 5. Implement relocation, recovery, and durable verification

Use the proven checkout mechanism from step 1. The intended sequence is:

1. Complete discovery/validation and calculate all destination and staging paths.
2. Save recoverable originals of every session file to be rewritten, including
   affected files outside the moving bucket. Check backup writes before moving
   the checkout. Preserve file modes and timestamps needed by the contract.
3. Print recovery-copy and staging locations before the first rename. Keep
   recovery data outside the moving checkout and session bucket.
4. Move the checkout. For the source-becomes-container case, first rename the
   checkout to an unused sibling staging location, create the container, then
   rename the staged checkout to its destination. Never try to rename a directory
   directly into itself.
5. Relocate its session collection and unchanged sidecars. Prepare transformed
   files beside their targets and publish individual replacements by rename.
6. Apply the recognized external inbound-reference edits with the same backup
   and per-file replacement discipline.
7. Verify the durable result before emitting completion output.

Do not assume these steps are atomic as a group. Track completed steps during
execution and report actual remaining locations and recovery actions on failure.
Leave originals and recovery copies available; do not add a resume command,
transaction journal, automatic cleanup service, or history repair mechanism.
An abrupt process termination cannot be promised a printed error or automatic
rollback; documented recovery must account for retained staging and backup data.

Durable verification checks:

- The checkout's supported Git/JJ arrangement remains usable and its preserved
  history/working-copy state matches the pre-move inventory.
- Session IDs, names, entry IDs/tree links, and non-location content match.
- Sidecar bytes, including orphan sidecars, match.
- Session ownership and encoded storage location agree with the destination.
- Recognized mapped references point at the correct relocated files, including
  the external DTCC references; unrelated references remain untouched.
- Session file times relevant to recency ordering are preserved.

**Gate:** normal and source-becomes-container fixture relocations both pass.
Introduce focused failure injection at backup, checkout staging, session
publication, external-reference update, and verification boundaries. Every case
retains recoverable data and reports partial work truthfully. Avoid a broad
mock-filesystem abstraction solely to inject these failures.

## 6. Finish CLI output and integration tests

- Render the agreed scope summary, progress, configuration warnings, recovery
  locations, and durable-verification result without conversation contents.
- Exit 0 for a valid preview or completed durable relocation; exit 1 for refusal,
  mutation failure, or failed durable verification.
- Print `No changes made` only when true. Do not advise rerunning a partially
  completed relocation without recovery.
- After a successful actual relocation, emit this as the final output, with the
  actual destination substituted:

  ```text
  Reminder: start Pi in <destination> and use /session-index to rebuild the session index.
  ```

- Finish all normal stdout/stderr output and cleanup that could report errors
  before that reminder. No success reminder on dry-run or failure.
- Do not touch the index database, broker, sockets, external config, or the
  `pi-sessions` package. No Node adapter or extension change is needed.

Add command-level integration tests for:

- `--help`, argument errors, relative/absolute paths, and paths with spaces.
- A complete dry-run and relocation using an isolated fixture home/storage.
- The two-step family layout: Perry to root, then Engine1 into the family.
- External reference changes without moving the external session.
- No writes to fixture configuration, index, or messaging state.
- Exit behavior, pre-mutation refusal versus partial failure, and the final
  reminder being the last observable stdout/stderr output on success.

**Gate:** build, lint, and the full test suite pass. Check destination-session
lookup compatibility with installed Pi on disposable fixtures, not only with
relo's own parser. Document usage, prerequisites, supported versions/layouts,
recovery instructions, manual configuration adjustments, and index rebuilding
in `README.md`. Implementation can be reviewed without touching real projects.

## 7. Separately authorize the first real use

This is an operator-controlled acceptance step, not part of implementation:

1. Review the implementation and fixture-test results.
2. Stop affected sessions/processes and run from outside the moving checkout.
3. Authorize and inspect a real `relo --dry-run` using the current inventory;
   inspection-time counts are not frozen inputs.
4. Explicitly authorize the actual move. Do not treat a successful preview or
   this plan as permission to move Perry or Engine1.
5. Review reported external configuration adjustments separately. For `mini`,
   distinguish the local selector/staging path from the remote working directory.
6. Manually rebuild through `/session-index` after the desired relocations.
7. Verify `pi -r` at the destination, resume existing conversations, and check
   working directory, names, navigation/bookmarks, and extension relationships.

**Done:** the durable relocation passes automated verification and the operator
confirms the rebuilt index and interactive resume behavior. Retain recovery
copies until the operator chooses to remove them.

## Implementation results and validation limits

The Go implementation uses only the standard library. `cmd/relo` owns argument
parsing/output; `internal/relocate` owns discovery, narrow transformations,
execution/recovery, and durable verification. `internal/moveprobe` contains the
independent disposable-checkout proof for step 1. `README.md` documents usage,
support limits, manual configuration/index work, and recovery.

Automated gate evidence:

- **Step 2:** canonical Pi `build`, `lint`, and `test` tools succeed through the
  Justfile. Help/usage/exit behavior is covered by `TestRunHelpAndUsage`.
- **Step 3:** `TestRunDryRunDoesNotWrite`, the family preview in
  `TestRunTwoStepFamilyLayout`, and `TestDiscoverReadOnlyAndExecute` compare
  before/after inventories, including a Perry/Engine1/DTCC fixture. Refusal
  tests cover collisions, mixed ownership, misleading prefixes, malformed
  affected metadata, storage customizations, symlink/aliased inputs, and
  unsupported VC arrangements. Dry-run creates no recovery or staging data.
- **Step 4:** `transform_test.go` covers all branches, external inbound records,
  legacy field preservation, large numeric literals, long records, newline
  handling, unrelated references, and byte-identical historical content.
- **Step 5:** Git-only and colocated-JJ relocations pass for both sibling and
  direct-child/container destinations. `TestFailureRecoveryBoundaries` injects
  backup/partial-backup, staging/staged-checkout, publication/partial-publication,
  external-reference, and verification failures, checking recoverable originals
  and truthful locations. `TestVerificationDetectsCorruption` confirms a real
  durable inventory mismatch fails verification. Modes and nanosecond session
  modification times are checked; sidecars/orphans remain byte-identical.
- **Step 6:** command tests cover paths with spaces, relative/absolute operands,
  two-step family layout, DTCC edits without moving DTCC, protected fixture
  configuration/index/messaging state, refusal versus post-backup failure, and
  the final reminder being last across shared stdout/stderr.
  `TestRunInstalledPiSessionDiscovery` successfully imports installed Pi 1.5.2
  and checks `getDefaultSessionDir`, `SessionManager.findById`, and
  `SessionManager.list` on the synthetic collection before and after relocation.
  No Node adapter or extension package changes are part of the product.

**Validation limit:** the final full test run passes with one explicit skip:
`TestCrossDeviceRefusedBeforeWrites` cannot exercise a second mounted device
because the fixture directory and `/` share a device. Device-ID validation is
implemented before writes, including the container move's sibling staging
location; there is no copy/delete fallback. No volume was mounted or real
cross-device move attempted for testing. Linux runtime behavior and interactive
Pi/index acceptance have not been exercised here.

Installed-source compatibility evidence (read-only inspection):

- Pi 1.5.2 `dist/config.js` defines `PI_CODING_AGENT_DIR` and
  `PI_CODING_AGENT_SESSION_DIR`, and default `~/.pi/agent` storage.
  `dist/core/session-manager.js` implements the encoded cwd bucket, header
  discovery, recency ordering, `findById` and `list`.
  `dist/core/settings-manager.js` reads global and project `settings.json` and
  the `sessionDir` setting. Unsupported explicit customizations are refused;
  past CLI-only `--session-dir` usage remains an operator prerequisite.
- Toolbox `index.js` pairs `<stem>.jsonl` with `<stem>.sidecar.json`; the inspected
  sidecar schema contains navigation/ownership identifiers, not location fields.
- pi-sessions 0.23.1 `extensions/subagents/ledger.ts` and
  `extensions/session-handoff/metadata.ts` define the supported operational
  paths. `extensions/shared/legacy-entry.ts` upgrades `goal`, `requestResponse`,
  and `nextSteps` on read; relo preserves their original durable spellings.
- The installed `extensions/shared/lineage.ts`, handoff `receipt.ts` /
  `board-loading.ts`, and subagent `wake.ts` / handoff `spawn.ts` show additional
  operational dependencies outside the agreed rewrite scope. Concrete mapped
  lineage references, non-subagent handoff receipts, and affected path-dependent
  resume commands (including the installed apostrophe-quoting representation)
  are therefore reported and refused, not silently rewritten. Historical
  subagent receipts remain unchanged as required.

No real Perry/Engine1 checkout, real conversations, derived index, broker,
sockets, external configuration, or installed package was changed. Step 7 still
requires separate authorization, index rebuilding, and interactive acceptance.
