# Track-a-doodle
A tool for visualizing, filtering and viewing GPS located activities on a map

See [docs/tech-stack.md](docs/tech-stack.md) for the proposed tech stack and architecture.

## Running locally

Requires Docker with Compose. Nothing else needs to be installed on the host.

```bash
./dev.sh
```

This rebuilds every module and recreates all containers, keeping the database. Run it again after pulling, switching branches or changing dependencies. Then open http://localhost:5173, sign up, and import GPX files with the button, or drag and drop them onto it.

Sign-up emails aren't delivered anywhere locally: [Mailpit](https://mailpit.axllent.org/) catches them, and they show up at http://localhost:8025. Open the confirmation link from there. See [Accounts](#accounts).

Other commands (`./dev.sh help` lists them all):

| Command | What it does |
|---|---|
| `./dev.sh down` | Stop the stack (keeps data) |
| `./dev.sh logs [service]` | Follow logs |
| `./dev.sh test` | Backend tests, including database tests, plus the frontend type check |
| `./dev.sh import [dir] [-user email] [-allow-duplicates]` | Batch-import GPX files from `dir`, or [samples/](samples/) by default, for a user who has signed up |
| `./dev.sh migrate [cmd]` | Run [goose](https://github.com/pressly/goose) on the database, e.g. `status`, or `create add_thing sql` for a new migration |
| `./dev.sh reset-db` | Delete all data and recreate the database, after asking |

Frontend source is mounted into its container, so edits reload live. Backend changes need `./dev.sh`.

To batch-import a folder, sign up in the app first, then run `./dev.sh import ~/path/to/gpx -user you@example.com` (subfolders are searched too), or put the files in [samples/](samples/) and run `./dev.sh import`. Files already imported are skipped, so this is also an easy way to import everything again after `./dev.sh reset-db`. The folder must be one Docker can mount (on macOS, anything under your home folder is shared by default; `/tmp` is not).

An activity's sport comes from the GPX track's `<type>`. Files without one get a sport from a word like "run", "ride" or "hike" in a track's `<name>`, and failing that in the filename (e.g. `Morning_Run.gpx`).

Running it again is safe; see [Duplicate detection](#duplicate-detection).

Files without a GPS track (indoor rides, treadmill runs, gym sessions) aren't imported, since there is nothing to show on the map. The UI and the batch command report how many were skipped and their activity types. Points at 0,0 count as missing a GPS fix.

Files that can't be read are reported as failed and nothing from them is stored, while the rest of the batch is imported as usual. Besides files that aren't valid GPX, this covers points whose latitude or longitude isn't a number or is outside -90 to 90 / -180 to 180, and timestamps more than 68 years apart. The UI lists each failed file with the reason, for example which point is wrong, and so does the batch command.

### Duplicate detection

Each import is checked against the user's own activities (never other users'):

- **Already imported:** a byte-identical file (same SHA-256) is always skipped.
- **Possible duplicate:** the file is *not* imported if an existing activity starts within a few seconds of it, and its distance and duration are both within a few percent of the larger value. Duration is skipped when either side has no timestamps.

GPS points aren't compared, because Garmin, Strava, Runkeeper etc. record and resample them differently. Files without timestamps can only be detected as already imported.

Possible duplicates can still be imported on purpose. The UI lists them side by side with the matching activity, explains the rules, and lets the user tick the ones to import anyway. The batch command takes `-allow-duplicates`.

The tolerances are the constants `DuplicateStartTolerance` and `DuplicateRelativeTolerance` in [backend/internal/importer/duplicates.go](backend/internal/importer/duplicates.go), and the comparison is `activity.Compare` in [backend/internal/activity/duplicate.go](backend/internal/activity/duplicate.go). The frontend reads the tolerances from `GET /api/config` and shows the backend's comparison for each possible duplicate, so changing them there also updates what users are told.

### Shared configuration

Values that more than one layer depends on are defined in the backend and served from `GET /api/config` (see [backend/internal/api/config.go](backend/internal/api/config.go)), which the frontend loads before it renders: the tile URL, layer name and zoom range, the accepted import file extensions, the duplicate rule, and the sport ids the backend recognizes. The frontend decides how sports look (labels and colors) for the ones it has a style for.

### Services

| Service    | Module                     | Host port        | Notes |
|------------|----------------------------|------------------|-------|
| `frontend` | [frontend/](frontend/)     | `127.0.0.1:5173` | Vite dev server (React + MapLibre GL JS), proxies `/api` to the backend |
| `backend`  | [backend/](backend/)       | `127.0.0.1:8080` | Go API: auth, GPX import, tile proxy |
| `martin`   | [mapserver/](mapserver/)   | not published    | Martin vector tile server |
| `db`       | [postgis/](postgis/)       | `127.0.0.1:5433` | PostgreSQL 17 + PostGIS |
| `mailpit`  | –                          | `127.0.0.1:8025` | Catches the backend's emails; web UI only, nothing is delivered |

Connect with psql: `psql postgres://trackadoodle_owner:dev-owner-pw@localhost:5433/trackadoodle`.
Defaults can be overridden with environment variables or a `.env` file (see [compose.yaml](compose.yaml)).

### Database migrations

The schema lives in [postgis/migrations/](postgis/migrations/) as SQL files applied by [goose](https://github.com/pressly/goose) (MIT). The `migrate` service runs them as the schema owner whenever the stack starts, before Martin and the backend; the backend only connects as `trackadoodle_app`, which can read and write rows but not change the schema. Only the database roles are still created by [postgis/init/](postgis/init/), when the volume is first created.

To change the schema, add a migration with `./dev.sh migrate create describe_change sql`, write the SQL under `-- +goose Up` (and `-- +goose Down`), then run `./dev.sh up` or `./dev.sh migrate`. Never edit a migration that has already been applied. `./dev.sh reset-db` is only needed to start over.

Databases created before migrations existed are adopted as they are: the first three migrations only create what is missing.

Activities imported in dev mode belong to `DEV_USER_EMAIL`, which has no password. To see them after signing up, import the files again with `-user`.

## How users are kept apart

A user must never see another user's tracks. The data path is built around that:

1. The browser only talks to the backend. Martin has no published port and no authentication of its own, so it is only on the `data` network in [compose.yaml](compose.yaml), together with the database and the backend. The frontend container is only on the `web` network and cannot reach either of them.
2. The backend resolves the user from the session cookie (see [Accounts](#accounts)) and calls Martin with that user's id. Nothing from the client's query string is forwarded.
3. `activities_tiles()` in PostGIS returns no tile without a valid `user_id`, and only that user's activities with one.
4. Martin's database role can execute that function and nothing else. It cannot read the tables directly. The function does trust the `user_id` it is given, though, so anything that can call Martin can read any user's tiles. That is why point 1 matters.

## Accounts

With `AUTH_MODE=password` (the compose default) people sign up with an email address and a password:

1. **Sign up.** The backend stores the account unverified and emails a confirmation link. It answers the same whether or not the address already has an account; an existing owner gets a "you already have an account" email instead, and their account isn't touched. So sign-up can't be used to find out who has an account.
2. **Confirm.** The link (`/verify#token=…`) opens a page that asks for the password chosen at sign-up. Only someone who knows the password *and* can read the address's email can activate the account, so registering someone else's address with your own password gets you nowhere. A wrong password leaves the link usable. Signing up again sends a new link and disables the old one.
3. **Sign in.** Unknown emails and wrong passwords get the same error and take the same time. Unverified accounts are only told to confirm once the password is right.

Details:

- Passwords: argon2id ([backend/internal/password](backend/internal/password/)), 10–128 characters, no composition rules (NIST SP 800-63B).
- Tokens in links and cookies are random 32-byte values; the database only keeps their SHA-256, so reading it gives no way in.
- Sessions: an HttpOnly, SameSite=Lax cookie (`tad_session`), valid 30 days, deleted on sign-out. `http.CrossOriginProtection` rejects state-changing requests from other sites (CSRF).
- Logging: failed sign-ins are logged as warnings with the email as typed, the client IP and a reason the client never sees (`unknown_email`, `wrong_password`, `not_verified`, `no_password`, `invalid_input`). Failed confirmations are logged too (`invalid_token`, `wrong_password`, without the token), and so is every rate-limited request. Successful sign-ins and confirmations are logged at info level. Passwords and tokens are never logged.
- Against sign-up spam, without a CAPTCHA so far: per-IP limits (5 sign-ups an hour, 10 sign-in or confirm attempts a minute), at most one email a minute and five a day per address, and a global cap on emails per hour. Unconfirmed sign-ups are deleted after 7 days.

Backend settings in password mode (see [compose.yaml](compose.yaml) for the local values):

| Variable | Meaning |
|---|---|
| `APP_BASE_URL` | Where users open the app; the base of links in emails |
| `SMTP_ADDR`, `SMTP_TLS` | SMTP server `host:port`, and `starttls` (default), `tls` or `none` (local catcher only) |
| `SMTP_USER`, `SMTP_PASSWORD` | Optional SMTP credentials |
| `MAIL_FROM` | Sender, e.g. `Track-a-doodle <noreply@example.com>` |
| `MAIL_MAX_PER_HOUR` | Cap on all emails sent (default 100) |
| `COOKIE_SECURE` | `false` only for plain-HTTP localhost (default `true`) |
| `TRUST_PROXY_HEADERS` | `true` when a proxy that sets `X-Forwarded-For` is the only way in |

`AUTH_MODE=dev ./dev.sh` still treats every request as `DEV_USER_EMAIL`, with no sign-in. Never use it outside local development. OIDC (Zitadel/Strava) can come later, next to passwords.

## Basemap

The default basemap is [OpenFreeMap](https://openfreemap.org/) ("Liberty" style). It's free with no API key or usage limits. The data is © OpenStreetMap contributors under the ODbL and the schema is OpenMapTiles, so attribution is required. The style shows it in the map's attribution control.

## Development

`./dev.sh test` runs everything without local Go or Node. If the stack isn't running, the database tests are skipped. To work on the frontend with a local Node instead:

```bash
cd frontend && npm install && npm run typecheck
```
