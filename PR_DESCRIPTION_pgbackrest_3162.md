## Summary

Clarifies how to perform a **full, self-contained pgBackRest restore** (including S3-backed repositories) in PGO, addressing confusion raised in issue #3162.

This PR adds practical guidance in `docs/TROUBLESHOOTING.md` under the restore section, including:

- Full restore via **new cluster bootstrap** using `spec.dataSource.postgresCluster`
- Full **in-place restore** via `spec.backups.pgbackrest.restore`
- Notes on physical restore semantics vs `pg_dump` logical restore semantics
- Explicit guidance that PITR flags are optional and not required for a standard full restore

## Problem

Issue #3162 asks whether PGO’s pgBackRest integration can support a full backup/restore workflow (especially with S3) similar in intent to `pg_dump`/`pg_restore`, without needing PITR-focused workflows.

While the operator supports this already, the troubleshooting/docs path lacked a concise, direct “here is how to do full restore” explanation and examples in one place.

## Changes

### Documentation updates

Updated:

- `docs/TROUBLESHOOTING.md`

Added a new subsection:

- **Full Backup/Restore with pgBackRest (S3-Compatible Repositories)**

It includes:

1. **Option A**: create a new cluster from existing pgBackRest backups with `spec.dataSource.postgresCluster`
2. **Option B**: perform in-place restore with `spec.backups.pgbackrest.restore`
3. Explicit notes for:
   - physical restore behavior
   - when PITR options are/are not needed
   - importance of selecting correct `repoName`

## Why this approach

- Minimal, low-risk change that improves operator usability immediately
- Keeps behavior unchanged; only clarifies supported workflows
- Aligns directly with the issue’s S3 and full-restore use case

## Validation

Documentation-only change.

Validation performed:

- Checked CRD/API fields used in examples:
  - `PostgresClusterDataSource.repoName/options`
  - `PGBackRestRestore.enabled` + inlined restore source fields
  - `PGBackRestRepo.s3`
- Confirmed consistency with existing restore e2e test manifests under:
  - `testing/chainsaw/e2e/pgbackrest-restore/templates/`

## Backward Compatibility

No API or runtime behavior changes.

## Follow-up (optional)

- Add a short “full restore quickstart” page under official backup/disaster-recovery docs with equivalent examples and links from README/tutorials.

## Related Issue

- Closes #3162

Co-Authored-By: Oz <oz-agent@warp.dev>
