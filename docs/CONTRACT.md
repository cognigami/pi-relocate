# Checkout and Pi history relocation contract

Status: agreed. Implementation and actual relocation have not been authorized by
this agreement.

## Purpose

Move an existing checkout and its Pi conversations to a new absolute path, so
that starting Pi in the destination and running `pi -r` offers the same sessions
and resumes them in the relocated checkout.

This is relocation, not copying, forking, workspace creation, or VC-history
repair.

## Canonical example

Before:

```text
~/Documents/perry/       # Original checkout
~/Documents/engine1/     # Concurrent checkout
```

After two relocations:

```text
~/Documents/perry/       # Container only
    root/               # Original perry checkout and its Pi sessions
    engine1/            # Original engine1 checkout and its Pi sessions
```

The first relocation puts a checkout beneath its former path. This must be
supported: the old path becomes a container, not the working directory for its
old sessions.

## Unit of relocation

One operation accepts an old checkout path and a new checkout path. It relocates:

- The complete checkout, including untracked and ignored files.
- Every existing Pi session whose recorded working directory is that exact old
  checkout path, across all branches of each conversation tree.
- Toolbox `.sidecar.json` files alongside the session collection, unchanged,
  including existing orphan sidecars. Relocation is not a cleanup operation.
- The known metadata references needed to discover and resume those sessions at
  the new path.

Sessions belonging to nested directories, sibling checkouts, or other working
directories are not implicitly included. Relocating a family is multiple
operations, not a recursive interpretation of session ownership.

## Required preservation

After a successful relocation:

1. The checkout's files and working-copy state are preserved.
2. VC commits, changes, branches, and bookmarks are unchanged. Only location
   metadata required by the supported checkout mechanism may change.
3. Pi sessions retain their IDs, names, timestamps, messages, conversation-tree
   structure, and non-location session data. Recency ordering must not change
   merely because metadata was rewritten.
4. `pi -r` in the destination discovers the same session collection previously
   associated with the source, without duplicate or newly forked sessions.
5. Resuming any relocated session uses the destination as its working directory
   and resolves project configuration and tools from there.
6. Recognized metadata references to relocated session files, including
   parent-session references, remain valid. This includes references from other
   sessions if their target file moves; unrelated session data remains unchanged.
7. Old sessions no longer identify the source as their working directory, even
   when the source path still exists as the new family container.

Historical prompts, responses, tool arguments, tool output, and compaction
summaries remain unchanged. This includes historical message endpoint snapshots,
reports, and arbitrary stored research data. An old absolute path appearing in a
conversation is not by itself an instruction to rewrite it.

## Supported metadata and extension handling

The first implementation covers the observed core Pi, Toolbox, and `pi-sessions`
formats, not every extension or handoff variant.

### Durable metadata

Update only recognized operational location fields whose targets move:

- Core session-header `cwd` and `parentSession`.
- `pi-sessions.subagent_launched` custom-entry `data.childSessionFile` and
  `data.cwd`.
- `pi-sessions.handoff-bootstrap` custom-entry `data.parentSessionFile`.

Apply these updates across all conversation-tree branches, including recognized
inbound references in sessions that are not themselves moving. Moving a parent
session does not imply moving children in unrelated directories. Preserve IDs,
writer ownership, legacy field representations, and all other entry data.

In the inspected setup, one `dtcc` session has both a core parent reference and a
handoff-bootstrap parent reference into Perry. That session stays where it is;
only those references change.

Toolbox sidecars contain tree/navigation identifiers rather than location fields
and must move without content changes.

Do not rewrite `pi-sessions.message_received` endpoint snapshots or other
historical event data. Existing matching subagent tool-result receipts also stay
unchanged: the inspected operational consumers use the custom launch ledger,
not those receipts. General support for other handoff variants, including
operational tool-result details or lineage formats not observed here, is outside
the first implementation's compatibility guarantee. Report any identified
unsupported operational dependency rather than claiming it was preserved.

### Derived index and runtime state

After durable metadata relocation, the operator manually refreshes the
`pi-sessions` derived session index using its existing rebuild mechanism
(`/session-index`, then rebuild). The last output from a successful actual
`relo` relocation must remind the operator to perform this rebuild, naming the
destination. All other output precedes that reminder.

The index must reflect relocated sessions and affected inbound relationships
before extension search and rosters are considered verified. Do not manually
rewrite the index database or historical text/file-touch evidence.

No messaging broker, socket, or other live-presence state migration is required.

### External configuration

Report identified external path-dependent configuration for an explicit separate
adjustment; do not silently rewrite it as part of relocating session metadata.

The known case is
`~/.config/pi-extensions/sandbox/99-mini.jsonc`: Perry's `mini` tool has a
project selector matching the old path and its descendants, plus a local staging
path inside the checkout. The selector must not inadvertently apply to sibling
checkouts after the family-layout move. Its remote working-directory argument
names a path on another machine and must not automatically follow the local
relocation.

These external configuration adjustments are disclosed separately, not included
in an unconditional claim that every integration remains unchanged.

## Preconditions and refusal behavior

- Pi sessions and other processes using the affected checkout or session files
  must be stopped before relocation. The tool is not a live-session mover and
  need not implement process discovery.
- The source must be an existing checkout of a supported type.
- The destination must not already exist. The tool does not merge checkouts or
  overwrite an existing destination.
- Existing destination Pi session files must never be overwritten. Any session
  ID or file collision that prevents an unambiguous relocation is an error.
- A checkout with location-dependent VC registration must use a supported
  relocation or repair mechanism. Unknown arrangements must be refused, not
  moved on the assumption that an ordinary directory rename is sufficient.
- If required session metadata cannot be understood safely, report the affected
  files and stop before mutation rather than silently skipping conversations.

## Failure and recovery

Before mutation, establish the affected checkout, session collection, and known
references, and check for conflicting destinations.

Keep recoverable originals of session files before rewriting them. Never remove
the only recoverable copy of either checkout data or session data.

A filesystem move and multiple metadata edits are not assumed to be atomic. On
failure, report what completed, where the recoverable data is, and what remains
to restore or finish. Do not report success for a partially relocated project.
No general transaction framework is required by this contract.

## Verification of success

Verify that:

- The checkout is usable at its destination, with the same working-copy state.
- The expected session IDs, conversation trees, and unchanged Toolbox sidecars
  are preserved, including existing orphan sidecars.
- Pi's destination-directory session lookup returns the expected collection.
- Recorded working directories and recognized session-file references resolve to
  the intended relocated paths, including inbound references from other sessions.
- The rebuilt extension index reflects the relocated sessions and affected
  relationships.
- Identified external configuration adjustments and compatibility limitations are
  reported explicitly.

An interactive `pi -r` and resume at the destination is the acceptance check for
the first real relocation.

## Explicit non-goals

- Creating or converting Git worktrees or JJ workspaces.
- Changing the operator's branching, bookmark, or checkout workflow.
- Repairing or restructuring VC history.
- Forking, cloning, exporting, compacting, or renaming conversations.
- Rewriting historical text to make old paths look current.
- Repairing arbitrary absolute paths in project files, virtual environments,
  build artifacts, external applications, or extension-owned state beyond the
  explicitly supported metadata above.
- Building a general extension-state migration framework.
- Introducing a project registry, task system, or ongoing synchronization service.

## Confirmed initial scope

- Independent clones: `engine1` was created with `git clone`, not as a linked
  worktree or workspace. Both inspected checkouts contain colocated `.git` and
  `.jj` directories; their existing Git/JJ setup must be preserved. Creating,
  converting, or relocating additional linked worktrees/workspaces is not
  required for the first implementation.
- Default Pi session storage. No `--session-dir`,
  `PI_CODING_AGENT_SESSION_DIR`, or `sessionDir` customization is in use.
  Custom session-storage support is not required for the first implementation.

Read-only inspection established the extension-handling requirements above.
Neither checkout, its histories, nor external configuration was changed during
inspection.

The interface, packaging, and internal move sequence are implementation decisions,
not additional requirements of this contract.
