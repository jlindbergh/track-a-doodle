# TODO

## Database migrations (soon)

The SQL in [postgis/init/](postgis/init/) only runs when the database volume is first created, so every schema or tile-function change needs `./dev.sh reset-db`, which deletes all data. That stops working once there is data worth keeping.

- Pick a migration tool (for example goose, golang-migrate or tern) and run migrations from the backend or a separate step in `dev.sh`.
- Turn the current init scripts into the first migrations. Roles and passwords (`010_roles.sh`) probably stay an init step.
- The simplification tolerances in the `geom_mid`/`geom_lo` generated columns are part of the schema, so tuning tiles will also need a migration.

## Accounts

Email + password sign-up and sign-in exist (see the README's Accounts section). Still missing:

- **Password reset.** Email a one-time link; `email_tokens.purpose` already leaves room for a `'reset'` purpose. End all the user's sessions when the password changes.
- **Change password and email** while signed in.
- **Session list and "sign out everywhere".**
- **Bot check on sign-up** if rate limits aren't enough, e.g. [ALTCHA](https://altcha.org/) (self-hosted proof of work, MIT).
- **Rate limits are per backend process**, in memory. Move them to the database or Redis if there is ever more than one backend.

## Generate TypeScript types from the Go API types

The API types in [frontend/src/api.ts](frontend/src/api.ts) are written by hand to mirror the Go structs (`activity.Facts`, `activity.Summary`, `activity.Comparison`, `importer.Result` and its statuses, the `/api/config` types), so they can drift apart without any error.

- Generate them from the Go structs, for example with [tygo](https://github.com/gzuidhof/tygo) added as a Go tool in `backend/go.mod`.
- Add `./dev.sh gen`, run in a Go container since nothing is installed on the host, and make `./dev.sh test` fail when the generated files are out of date.
- Check how `*time.Time`, pointers and embedded structs come out; struct tags may be needed.

## Code quality

Found in a review; none of these is a known bug today.

- **`store.InsertActivity` does too much.** It locks, finds the same file, inserts, decides on duplicates and rolls back ([store.go](backend/internal/store/store.go)). Move the duplicate decision out of the store. Replace the `nil` rule that means "allow duplicates" with an explicit option, and use one set of status names instead of both `Inserted`/`SameFile`/`PossibleDuplicate` and `imported`/`already_imported`/`possible_duplicate`.
- **`ImportPanel` does too much, and pairs results with files by index** ([ImportPanel.tsx](frontend/src/components/ImportPanel.tsx)). Pair them by the `file` the backend returns, or by a client id, since the backend skips parts without a file name. Use `setEntries(prev => …)` after `await`, and ignore drops while an import is running. Split the derived lists out of the component.
- **The tile SQL repeats magic numbers.** The extent `4096` and the buffer `64` appear three times in [030_tiles.sql](postgis/init/030_tiles.sql), and line 47 converts the buffer to metres. Name them and explain the conversion, so the bbox filter cannot drift from `ST_AsMVTGeom`.
- Smaller items:
  - `run()` in [cmd/import/main.go](backend/cmd/import/main.go) does four things.
  - The `as unknown as` casts in [TrackMap.tsx](frontend/src/components/TrackMap.tsx) have no comment.
  - `activity.Facts` is a vague name.
  - `format.ts` also holds the sport colors.
