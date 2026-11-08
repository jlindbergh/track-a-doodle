# Garmin Connect

How track-a-doodle gets activities from Garmin Connect, and why this way. Researched and checked on 2026-11-08.

## Ways in

Garmin's Connect Developer Program was closed to new applicants from about March 2026 and reopened around 21 October 2026 with three tiers:

| Tier | Who | Delivery | Historic data |
|---|---|---|---|
| **Personal** | Anyone. A self-service key from Garmin Connect, no approval | Pull only (26 GET endpoints) | Everything, checked (see below) |
| **Starter** (business) | Needs Garmin to approve an organisation (company, website, privacy policy). Reportedly free for up to about 1,000 users | Push webhooks only | None |
| **Enterprise** | Requested from Starter, terms set per company | Push, or ping and pull | Backfill of "one month or more", once per data type, only if the user allows historical export |

**We use the Personal API.** Track-a-doodle is a small app, so asking each user for their own key is acceptable. It is the only tier that reaches the whole history, and the only one available without an approved organisation. Garmin's FAQ says the Personal API is "not intended for commercial applications serving multiple end users". That fits a small, self-hosted instance; a large public one would need the business program and OAuth.

**Rejected: logging in with the user's Garmin password**, as the `garminconnect` Python library does. It imitates the Garmin Connect mobile app's login and uses `curl_cffi` to look like a browser to Cloudflare. We would have to store users' Garmin passwords and get around Garmin's bot detection, and it breaks whenever Garmin changes its login (it did in March 2026). It doesn't use the Personal API, which is why using it creates no key.

**The fallback is Garmin's account data export.** It holds every original FIT file, and track-a-doodle imports FIT files, so a user can import their history without a key (see the guide in the app).

## The Personal API

Base URL: `https://personal.connectapi.garmin.com`. The [swagger docs](https://developer.garmin.com/gc-developer-program/personal/swagger-docs/) list the endpoints.

- **Key.** The user creates it at <https://connect.garmin.com/app/settings/personalApiToken>, with an expiry of 30, 90 or 180 days or a year. It is shown once.
- **Token.** `POST /personalapitoken-service/personal-api/personal-api-key/token` with `Authorization: ApiKey <key>` returns a raw JWT, valid for about 24 hours. There is no refresh token, so the backend makes a new one from the key.
- **Activity search.** `GET /activitylist-service/activities/search/activities` with `Authorization: Bearer <token>`. Parameters:
  - `start` (offset) and `limit` (default 99). An empty array means there are no more.
  - `sortOrder` `asc`/`desc`, by local start time.
  - `startDate`/`endDate` as YYYY-MM-DD, local time.
  - `activityType`, `activitySubType`, and `excludedActivitySubType` (repeatable).
  - `requiredField` (repeatable; the field must not be null).
  - `excludeChildren`, and a bounding box.
- **Download.** `GET /download-service/files/activity/{id}` returns a ZIP holding the original FIT file.
- **Rate limits** aren't documented.

Checked with a real key:

- The token response is a raw JWT.
- The search returns summaries with `activityId`, `activityName`, `startTimeGMT`/`startTimeLocal` (`2026-11-08 10:30:35`), `activityType.typeKey`, `distance`, `duration`, `hasPolyline`, `startLatitude`.
- `?start=0&limit=1&sortOrder=asc&requiredField=startLatitude` returns the account's first activity with a GPS track, skipping an earlier strength workout without GPS. So **the whole history is reachable**, and `requiredField=startLatitude` leaves out activities without GPS.
- The download returns a zipped FIT file.

## How the sync works

The code is in [backend/internal/provider/garmin](../backend/internal/provider/garmin/) (the client) and [backend/internal/activitysync](../backend/internal/activitysync/) (the connection service and worker).

1. **Connect.** The user pastes their key into the import panel. The backend checks it by asking Garmin for a token. It then stores the key and the token encrypted with AES-256-GCM under `SECRETS_KEY`, each bound to the user, in `provider_connections`.
2. **Backfill**, only after the user agrees in a dialog that says what happens.
   - The worker pages through the search, oldest first, with `requiredField=startLatitude` and without treadmill, indoor cycling and virtual activities.
   - It downloads each FIT file and imports it under Garmin's activity name, with the usual [duplicate rule](../README.md#duplicate-detection). So activities already imported from GPX or FIT files are skipped.
   - Progress is saved per page. It resumes after a restart, can be stopped, and hands over to other connections after 10 minutes of work.
3. **Poll.** Every 30 minutes it fetches activities from a day before the latest one handled. Without a finished backfill, it only imports activities from after the user connected.
4. **Every activity handled is recorded** in `provider_activities`: imported, skipped or failed. None is downloaded twice, even after the user deletes it.

Requests are spaced at least 2 seconds apart. Throttling (429), server errors and network errors are retried with exponential backoff and Garmin's `Retry-After`. **Every failed attempt is logged** as a warning (`garmin request failed`) with the method, path, query, status or error, attempt number, retry delay and `Retry-After`. Keys and tokens are never logged. Grep the backend logs for these lines to see when and how often Garmin pushes back, and tune `MinInterval` in the client if needed.

When Garmin rejects the key (deleted, expired or wrong), syncing stops. The user is asked to create a new key and replace it; the backfill carries on from where it stopped.

## Not done yet

- Garmin OAuth with push webhooks, if a business application is ever approved.
- Uploading the Garmin export ZIP as it is, instead of unzipping it first.
- Health data (sleep, heart rate, and so on).
- Rotating `SECRETS_KEY`. Sealed values carry a version byte for that.

## Other services

| Service | Personal key? | Access | Full history | New-activity notifications |
|---|---|---|---|---|
| **Strava** | No (its settings token is read-only and lasts 6 hours) | A self-service OAuth app. "Single Player Mode" serves only you; you can raise it to 10 users yourself; more needs a review that can take months | Yes, by paging through the API | Webhooks |
| **Polar AccessLink** | No | A free, self-service OAuth app | No: only about 30–90 days before the user connects. Older data needs Polar Flow's export | Webhooks |
| **Suunto API Zone** | No | A partner application, about 2 weeks, aimed at companies | Yes, the workout list covers past workouts | Webhooks |

Strava fits best as the next service: up to 10 users need no review, and it has full history and webhooks. It would be a second provider in `provider_connections`.

## Sources

- [Garmin Connect Developer Program overview](https://developer.garmin.com/gc-developer-program/overview/)
- [Program FAQ](https://developer.garmin.com/gc-developer-program/enterprise/program-faq/)
- [Activity API](https://developer.garmin.com/gc-developer-program/enterprise/activity-api/)
- [Personal API](https://developer.garmin.com/gc-developer-program/personal/personal-api) and its [FAQ](https://developer.garmin.com/gc-developer-program/personal/personal-faq/)
- [the5krunner: developer API access paused](https://the5krunner.com/2026/09/14/garmin-developer-api-access-paused/)
- [the5krunner: Garmin Personal API](https://the5krunner.com/2026/10/22/garmin-personal-api/)
- [Garmin Rumors: Personal, Starter and Enterprise](https://garminrumors.com/garmin-connect-developer-program-personal-starter-enterprise/)
- [tamcore/garmin-mcp#42: Personal API endpoints](https://github.com/tamcore/garmin-mcp/issues/42)
- [Polar AccessLink](https://www.polar.com/accesslink-api/)
- [Suunto API Zone FAQ](https://apizone.suunto.com/faq)
- [Strava rate limits](https://developers.strava.com/docs/rate-limits/)
- [Terra: Strava API changes in 2026](https://tryterra.co/blog/strava-api-changes-2026)
