# Recovery review follow-ups

This follows three reviews of `87d0ca342764ffbe2d7b95703e397c735213d58f`.
Each original finding was read and reproduced. This is author verification, not reviewer
approval or thread resolution. Scope remains Web development and its Hub/Node backend
update chain, with three verified snapshots and program-only Apply backup. No native
client adaptation, deployment or production-data restoration is included.

| Finding | Reproduction | Repair and regression |
| --- | --- | --- |
| [Omitted platform package](https://github.com/nwverse/persona/pull/6923#discussion_r4580152346) | The full saved-version installer/confirmation test lost the unstaged second platform's normal package path when fleet automation was off. | Confirmation now passes the development candidate identity. Backup apply swaps staged platforms only; omitted regular packages stay in place with their original version identity, preserving existing labels. Fresh-build and release set-aside behavior stays separate. `TestDevelopmentAppliesOlderProgramBackupThroughSharedInstaller` verifies confirmation, and `TestDevelopmentBackupKeepsUnstagedPackageIdentity` covers explicit labels and invalid-marker failure with retained staging. |
| [URI via PGDATABASE](https://github.com/nwverse/persona/pull/6923#discussion_r4580152350) | Actual pg_dump 16.15 / libpq 160015 treated the full URI as a database name. Both URI and keyword backups failed using the immutable original helper in a private test cluster. The finding is applicable on this tested toolchain. | Use libpq `PQconninfoParse`, preserve its URI/query/keyword parsing and map explicit options to their documented environment variables; reject unsafe/unavailable bindings before writer pause. Full DSNs and passwords stay out of argv. The opt-in real PostgreSQL test verifies escaped credentials, query options/application name, actual pg_dump argv, successful custom dump/restore into a fresh synthetic database and unchanged source rows. |
| [Recovery metadata](https://github.com/nwverse/persona/pull/6923#discussion_r4580512578) | Scalar/list metadata could raise TypeError; a personad entry mapped to a valid Node archive could pass verification and select the wrong program. | One shared schema check for listing/verify/prepare validates required fields, primitive/container types, timestamps, original absolute paths, inventory, program/platform allowlists, exact expected artifact names and lowercase hashes. Payload verification checks actual files and decompressed hashes; preparation independently checks staged native bytes after the compressed checksum. Malformed entries raise ValueError, remain retained and are excluded from listings/rotation. |
| [Archive members/links](https://github.com/nwverse/persona/pull/6923#discussion_r4580512581) | Backslash/drive/UNC forms, hard links, duplicate paths and symlink-parent graphs could pass the previous archive checks. | Shared canonical POSIX member/link validation rejects those forms, absolute/traversal/noncanonical names and dangling/chained links. Only direct relative links to archived files/directories are allowed. The operator-only `extract-history` command uses this same validator and an empty destination, plus Python's data filter. Apply backup never calls extraction. Malicious archives are rejected before destination creation; legal synthetic history and its relative link restore successfully. |

| [Failed boot recovery](https://github.com/nwverse/persona/pull/6923#discussion_r4581680631) | A later review arrived during final verification. With no pending marker, both a missing preimage and failed exec made boot return true; the failed exec also removed the adoption journal. | Boot now returns recovery errors to both standalone and edge-slot entry points before config/database startup. Rollback propagates restoration/exec failures and retains development journal/staging until the restored executable runs. `TestDevelopmentCrashRecoveryFailureBlocksAttemptedBuild` covers both failures, retained material through pruning and successful retry after repair. Earlier cutpoints and release boot tests remain covered. |

## Real PostgreSQL evidence

The acceptance test uses existing PostgreSQL binaries to initialize and stop its own
small cluster. It binds only an owned private Unix socket, disables TCP and pins safe
PGHOST/PGPORT defaults even during the original-helper reproduction. Synthetic source
and restoration databases are created in that cluster. No existing database is used.
Passwords are generated per run, used through private files/environment, and checked
against helper output, receipts and the actual pg_dump command line. The table-lock
fixture verifies the URI/keyword application_name through pg_stat_activity, then releases
only its own lock connection and restores the dump into a fresh synthetic database.

Primary references: PostgreSQL's [connection parser](https://www.postgresql.org/docs/16/libpq-connect.html#LIBPQ-PQCONNINFOPARSE)
and [environment parameters](https://www.postgresql.org/docs/16/libpq-envars.html).
The test establishes behavior on the installed 16.15/libpq160015 toolchain, not every
PostgreSQL version or native macOS execution. Passwords in a child environment remain
visible to the installation account/privileged processes; this is not a new isolation
boundary. libpq must be discoverable beside the PostgreSQL tools. Unsupported parameter
bindings fail explicitly rather than falling back to putting secrets in argv.

```sh
PERSONA_TEST_POSTGRES_BIN=/path/to/postgresql/bin python3 -B -m unittest -v scripts/test/test_web_development_postgres.py
# Real PostgreSQL 16.15 / libpq 160015: 3 tests OK (URI/keyword dump-restore and invalid-parameter rejection before writer pause).
python3 -B -m unittest -v scripts/test/test_web_development_update.py scripts/test/test_web_development_recovery.py
# Existing cancellation/coordination/rotation/program-only fixtures plus malformed schema/archive tests.
cd server
go test ./internal/hub ./internal/update -run 'Test(Development|AFailedSwap|SwapProgram|HubOnlyUpdate|NodeUpdatePolicy)' -count=1 -timeout=3m
go test -race ./internal/hub ./internal/update -run 'Test(Development|AFailedSwap|SwapProgram|HubOnlyUpdate|NodeUpdatePolicy)' -count=1 -timeout=3m
```

The malformed-manifest test covers scalar roots, invalid container/field types, wrong
hash types, wrong artifact mapping, path escape and metadata list/verify/prepare
contracts. Archive cases cover absolute/traversal/backslash/drive/UNC paths, hard links,
symlink escapes/parents/chains, duplicates and canonicalization. A staged-file fault
between compressed checksum and native verification is rejected. Hard-linked history
fails snapshot creation without pruning the previous verified snapshot.

## Final delivery evidence and boundaries

The PR follow-up binds the precise final head, fresh command results and remote state.
Normal Web development compilation and tagged Linux backend compilation remain the
build gate; targeted fault/real-database tests address these findings. The original
crash, cancellation and partial-replacement regressions are rerun and preserved.
Web source tree `bc453ef2d738f54b53af919951c4f3b95eceb656` is unchanged, so the public
29-image [gallery](../pr-6923-web-update/README.md) and its image/source checksums still describe this UI.
The checked-in browser matrix is rerun on the final delivery head.


An additional architecture check fails because the pre-existing backup list/apply
routes are absent from the explicit Hub-owned route inventory. The route file,
guard and inventory are unchanged from `87d0ca342764ffbe2d7b95703e397c735213d58f`.
The requester explicitly disallowed raising guard baselines to remove failures;
no allowlist, guard logic, dependency/store limit or business-route exemption is
changed here. This failure remains disclosed separately from the passing focused
update regressions and normal compilation. A maintainer must settle the route
ownership declaration before treating the broader server guard as passing.

Retained snapshots are local recovery material, not authenticated artifacts against
an account able to replace both data and hashes. Old/corrupt journals and snapshots,
conflicting overrides, failed writer resume and SIGKILL still require operator
inspection. Program rollback does not undo database migrations; the existing idle
check is not an admission lock. Native macOS host runtime, real writer hooks, production
restoration, power-loss/filesystem fault acceptance and maintainer visual approval
remain outside this isolated evidence. No hosted CI success is inferred from empty checks.
