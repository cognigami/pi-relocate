# CLI design

Status: proposed UX for agreement. Implements the scope in `CONTRACT.md`; no
implementation or real relocation is authorized by this draft.

## Command

```text
relo [--dry-run] SOURCE DESTINATION
relo --help
```

One checkout per invocation. No subcommands, project registry, workspace creation,
or extension-management commands.

Example, run from outside both checkouts:

```sh
cd ~/Documents
relo --dry-run perry perry/root
relo perry perry/root
relo engine1 perry/engine1
```

The second real relocation is run only after the first completes its durable-file
stage. The index can be rebuilt once after both relocations.

## Paths and prerequisites

- Accept absolute paths or paths relative to the caller's working directory.
- Show resolved absolute source and destination paths in the operation summary.
- The destination is the exact new checkout path, not an existing container into
  which the source basename is appended. Existing destinations are refused.
- Create missing destination parents. Support the canonical move from `perry` to
  `perry/root`, with the old checkout path becoming the container.
- Run outside the source checkout. A caller whose working directory is inside
  the source must change directories first.
- The operator must stop affected Pi sessions and other processes before an
  actual move. The tool does not detect or stop them.
- Session storage is Pi's default location; there is no custom-storage flag in
  the first version.

## Preview

`--dry-run` discovers and validates the proposed move without modifying the
checkout, sessions, index, or configuration, or creating backup files.

Report:

- Checkout source, destination, and detected Git/JJ arrangement.
- Session-storage source and destination.
- Counts of sessions and sidecars to move, including orphan sidecars.
- Recognized operational metadata updates and any affected external session
  files, so changes outside the source project are visible.
- Identified unsupported dependencies or blockers.
- Known external configuration adjustments, including `mini` where applicable.
- The final index-rebuild and interactive acceptance steps.

Preview is optional, not a saved plan or authorization token. An actual invocation
recomputes its inputs and validates them before mutation; it does not trust an
earlier preview's inventory.

## Execution

Without `--dry-run`, print the same scope summary, perform validation, then carry
out the durable relocation. There is no confirmation prompt: invoking the
mutating command is the authorization. Do not add a redundant `--yes` option.

Print short progress messages for the checkout move, session relocation,
recognized reference updates, and verification of the durable files. Do not log
conversation contents or list every unchanged session.

On completion, report the new checkout and session locations, where recoverable
original metadata was kept, and any remaining external configuration adjustments.
Do not automatically delete recovery copies.

No `--force`, overwrite, merge, recursive-family, blanket-path-replacement, or
skip-verification options are provided.

## Explicit finalization step

Inspection of installed `pi-sessions` 0.23.1 confirms that its supported
`/session-index` rebuild entry point requires interactive Pi and explicitly
refuses non-TUI execution. Installed `pi --help` also exposes no session-index
rebuild option. Passing the slash command through `pi --print` does not bypass
that restriction.

Agreed: the operator performs the index rebuild manually. The CLI does not
import private extension internals, implement a replacement indexer, edit/delete
the index database, or require changes to `pi-sessions`.

After a successful actual relocation, print all progress, verification results,
recovery-copy locations, external configuration warnings, and acceptance-check
instructions before a final reminder. For example:

```text
Checkout and session files relocated; durable metadata verified.
...
Verify discovery and resume with pi -r in the destination.

Reminder: start Pi in <destination> and use /session-index to rebuild the session index.
```

Substitute the actual destination in the reminder. This reminder must be the
last output from a successful relocation: no subsequent summary, progress,
warning, or normal diagnostic output on stdout or stderr. It is not a success
message for a dry run or a failed relocation.

The rebuild can occur once after several completed relocations. End-to-end
acceptance under the contract remains pending until the rebuild and interactive
checks succeed; the CLI must not claim automatic index rebuilding or fully
verified extension behavior.

## Errors and exit status

- Exit 0: preview validated, or the durable relocation stage completed. For an
  actual move this does not claim the manual index/acceptance steps are complete;
  the output explicitly identifies them as pending.
- Exit 1: invalid invocation, refusal, failed relocation, or failed durable-file
  verification.

Before-mutation failures say `No changes made` only when that is true. Failures
after mutation identify completed steps, current checkout/session locations,
recovery-copy locations, and the remaining recovery actions. Never present a
partially completed durable relocation as successful or advise blindly rerunning
it.
