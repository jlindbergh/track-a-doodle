# Track-a-doodle
A tool for visualizing, filtering and viewing GPS located activities on a map. 


## Tech stack
Status **proposed**
- PostgreSQL + PostGIS (probably with `h3-pg` extension for hexagon summaries)
- Map tiles, served by Martin
- React/TS + Vite single page application 
- Frontend map rendering using MapLibre GL JS w/ deck.gl (react-map-gl/maplibre)
- Backend in go (stdlib or chi plus gpx/gpxgo probably)
- OICD login (Zitadel/Strava OAuth)
- Docker Compose (PostGIS + Martin + frontend) for easy dev setup

For further discussion about tech stack see [docs/tech-stack.md](docs/tech-stack.md)


## Coding conventions
### Backend
Follow official standards and best practices from https://dev.to/leapcell/go-coding-official-standards-and-best-practices-284k


### Frontend
Ensure adhering to the [react reference rules](https://react.dev/reference/rules)
Use npm audit when adding or removing packages and report if there are any dependencies with known vulnerabilities  


## Architecture
Let's go full on monorepo with this
- Keep database code and scripts in a separate module `postgis/`
- Map server/Martin configuration and code in `mapserver/`
- Backend base directory `backend/`
- Frontend base directory `frontend/`


## Rules
- A user **must** only be able to see their own imported data, and can under no circumstances be 
allowed to see someone else's tracks/routes

## Miscellaneous
- Use base maps from openstreetmap/OpenFreeMap or Swedish Land survey (Lantmäteriet, "minkarta") if possible. Make sure to analyze
licenses of each map service before suggesting adding it.
- Sign in with Garmin Connect option (or Strava)
- Option for manual batch import of gpx files
- Getting data imported and ready to view is a key flow of the application - no data, no fun!

### Git hygiene
- Use [conventional commits](https://www.conventionalcommits.org/en/v1.0.0/) for commit messages 
- Try to keep commits small so they're easy to review.
- Create and work in feature branches, do not merge to main without human intervention (I might regret this)

