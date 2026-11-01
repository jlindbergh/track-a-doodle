# Track-a-doodle
A tool for visualizing, filtering and viewing GPS located activities on a map

See [docs/tech-stack.md](docs/tech-stack.md) for the proposed tech stack and architecture.

## Running locally

Requires Docker with Compose. Nothing else needs to be installed on the host.

```bash
./dev.sh
```

This rebuilds every module and recreates all containers, keeping the database. Run it again after pulling, switching branches or changing dependencies. Then open http://localhost:5173 and import GPX files with the button, or drag and drop them onto it.

Other commands (`./dev.sh help` lists them all):

| Command | What it does |
|---|---|
| `./dev.sh down` | Stop the stack (keeps data) |
| `./dev.sh logs [service]` | Follow logs |
| `./dev.sh test` | Backend tests, including database tests, plus the frontend type check |
| `./dev.sh import [dir] [-allow-duplicates]` | Batch-import GPX files from `dir`, or [samples/](samples/) by default |
| `./dev.sh reset-db` | Delete all data and recreate the database from [postgis/init/](postgis/init/), after asking |

Frontend source is mounted into its container, so edits reload live. Backend changes need `./dev.sh`.

To batch-import a folder, run `./dev.sh import ~/path/to/gpx` (subfolders are searched too), or put the files in [samples/](samples/) and run `./dev.sh import`. Files already imported are skipped, so this is also an easy way to import everything again after `./dev.sh reset-db`. The folder must be one Docker can mount (on macOS, anything under your home folder is shared by default; `/tmp` is not).

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
| `db`       | [postgis/](postgis/)       | `127.0.0.1:5433` | PostgreSQL 17 + PostGIS (h3-pg installed, not enabled) |

Connect with psql: `psql postgres://trackadoodle_owner:dev-owner-pw@localhost:5433/trackadoodle`.
Defaults can be overridden with environment variables or a `.env` file (see [compose.yaml](compose.yaml)).

### Resetting the database

The SQL in [postgis/init/](postgis/init/) only runs when the database volume is first created. After changing it, run `./dev.sh reset-db`, which deletes all imported data.

## How users are kept apart

A user must never see another user's tracks. The data path is built around that:

1. The browser only talks to the backend. Martin has no published port and no authentication of its own, so it is only on the `data` network in [compose.yaml](compose.yaml), together with the database and the backend. The frontend container is only on the `web` network and cannot reach either of them.
2. The backend resolves the user from the session and calls Martin with that user's id. Nothing from the client's query string is forwarded.
3. `activities_tiles()` in PostGIS returns no tile without a valid `user_id`, and only that user's activities with one.
4. Martin's database role can execute that function and nothing else. It cannot read the tables directly. The function does trust the `user_id` it is given, though, so anything that can call Martin can read any user's tiles. That is why point 1 matters.

**Login does not exist yet.** With `AUTH_MODE=dev` every request is treated as `DEV_USER_EMAIL`, and the backend refuses to start in any other mode. OIDC (Zitadel/Strava) is the next step before this runs anywhere but localhost.

## Basemap

The default basemap is [OpenFreeMap](https://openfreemap.org/) ("Liberty" style). It's free with no API key or usage limits. The data is © OpenStreetMap contributors under the ODbL and the schema is OpenMapTiles, so attribution is required. The style shows it in the map's attribution control.

## Development

`./dev.sh test` runs everything without local Go or Node. If the stack isn't running, the database tests are skipped. To work on the frontend with a local Node instead:

```bash
cd frontend && npm install && npm run typecheck
```
