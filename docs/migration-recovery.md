# Recovering from a dirty migration

`puckdb db migrate` (the init container), the `MigrateDatabaseWorkflow`, and `db drop` all refuse to run when
the database is **dirty**. The Temporal activities report it as a non-retryable `DirtyMigrationError`, so the
workflow fails immediately instead of retrying:

```
database is dirty at migration version 2: a previous migration failed partway. Inspect the schema, then run
`puckdb db force-version <version>` with the version the schema actually matches
```

Dirty means a migration started and did not finish. golang-migrate sets the flag *before* running a migration's
SQL and clears it after, so a crash, a SQL error, or a killed pod leaves it set.

puckdb never repairs this on its own. The recorded version alone does not say what happened:

- **It does not give the direction.** A failed *up* to version 2 records `2, dirty`. A failed *down* from 2
  records `1, dirty` (the version it was heading to) while the schema still matches 2. A failed down of the
  *first* migration has no lower version to record, so it stores `-1, dirty`.
- **It does not say how much ran.** A migration containing `COMMIT`, or several statements the driver cannot
  wrap in one transaction, can be partially applied.
- **Versions are not guaranteed contiguous**, so "the previous version" is not `version - 1`.

## Steps

1. **Find out why it failed.** Read the logs of the run that failed (`puckdb-logs`, or the `db migrate` init
   container logs). The original SQL error is reported together with the dirty notice.

2. **Read the recorded state.**

   ```sql
   SELECT version, dirty FROM schema_migrations;
   ```

   `-1` is legitimate: it means "no migration recorded". `db drop` runs every down migration, so a failure in
   `000001_init.down.sql` leaves `-1, dirty`. The error then reads "dirty with no migration recorded (version -1)".

3. **Inspect the schema** and compare it with `internal/database/migrations/<version>_*.up.sql` and `.down.sql` for the
   dirty version and its neighbours. Decide which version the schema *actually* matches. Check every object the
   migration touches (columns, indexes, views), not just the first one.

4. **If the schema sits between two versions**, finish or undo the partial work so it matches exactly one of
   them. Do this through a corrected migration where possible. If manual SQL is unavoidable, make it bring the
   schema to precisely the state of a defined version.

5. **Record that version.** This runs no migration SQL; it only clears the dirty flag at the version you name.

   ```bash
   puckdb db force-version <version>     # 0 = no migration applied
   ```

   It refuses a database that is not dirty, and a version the embedded migrations do not define.
   In the cluster, run it as a one-off pod or job using the same image and env as the `db migrate` init container.

6. **Run migrations again.**

   ```bash
   puckdb db migrate
   ```

## Worked examples

| Recorded | What happened | Schema matches | Action |
|----------|---------------|----------------|--------|
| `2, dirty` | `000002` up failed, fully rolled back | 1 | `force-version 1`, fix the migration, `db migrate` |
| `2, dirty` | `000002` up failed after creating some objects | neither | finish or undo the partial objects, then force the version you reached |
| `1, dirty` | `000002` down failed, fully rolled back | 2 | `force-version 2`, fix the down migration |
| `1, dirty` | `000002` down dropped some columns then failed | neither | data in dropped columns is gone; restore from backup or complete the down by hand, then `force-version 1` |
| `-1, dirty` | `000001` down failed, fully rolled back | 1 | `force-version 1`, fix the down migration, retry the drop |
| `-1, dirty` | `000001` down dropped some objects then failed | neither | finish dropping the remaining objects, then `force-version 0` |

## Throwaway databases

For a test or scratch database it is simpler to drop and recreate the database than to repair it.
