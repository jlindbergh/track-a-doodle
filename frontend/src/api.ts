/** The values used to tell activities apart; activity.Facts in the backend. */
export interface ActivityFacts {
  id?: string;
  name: string;
  sport: string;
  startedAt: string | null;
  durationS: number | null;
  distanceM: number;
}

/** A stored activity; activity.Summary in the backend. */
export interface Activity extends ActivityFacts {
  id: string;
  /** [west, south, east, north] in WGS 84. */
  bbox: [number, number, number, number];
  /** The name of the imported file, if known. */
  sourceFilename: string | null;
  importedAt: string;
}

/** How two activities differ, as computed by the backend. */
export interface Comparison {
  /** Seconds between the start times, or null if either is unknown. */
  startDiffS: number | null;
  /** Fraction of the longer distance. */
  distanceDiff: number;
  /** Fraction of the longer duration, or null if either is unknown. */
  durationDiff: number | null;
}

export interface ImportResult {
  file: string;
  status: 'imported' | 'already_imported' | 'possible_duplicate' | 'no_gps_track' | 'failed';
  activityId?: string;
  error?: string;
  /**
   * The activity read from the file. For no_gps_track only name and sport are
   * meaningful, and name is "" if the file has none.
   */
  activity?: ActivityFacts;
  /** The already imported activity it matched. */
  existing?: ActivityFacts;
  /** How activity and existing differ, for possible_duplicate. */
  comparison?: Comparison;
}

/** How the backend detects duplicates; see backend/internal/importer/duplicates.go. */
export interface DuplicateRules {
  startToleranceS: number;
  /** Fraction, e.g. 0.02 for 2%. */
  relativeTolerance: number;
}

/** What the frontend needs to know about the backend; see backend/internal/api/config.go. */
export interface AppConfig {
  tiles: {
    /** URL template for the signed-in user's activity tiles, relative to the origin. */
    url: string;
    sourceLayer: string;
    minZoom: number;
    maxZoom: number;
  };
  import: {
    /** Lowercase, with the dot, e.g. ".gpx". */
    acceptedExtensions: string[];
    duplicateRule: DuplicateRules;
  };
  /** The sport ids the backend recognizes. */
  sports: string[];
  auth: {
    /** "password": users sign in. "dev": every request is one development user. */
    mode: 'dev' | 'password';
    passwordMinLength: number;
    passwordMaxLength: number;
  };
  /** The activity services users can connect to. */
  connections: {
    garmin: boolean;
  };
}

/** Progress of importing everything from a service; activitysync.Backfill in the backend. */
export interface BackfillStatus {
  status: 'not_requested' | 'running' | 'done' | 'failed' | 'cancelled';
  consentedAt: string | null;
  /**
   * Start of the latest activity from before connecting that has been handled. The backfill goes oldest
   * first, so everything up to it is done. Null before the first one.
   */
  reachedAt: string | null;
}

/** What came of the activities handled from a service; activitysync.ActivityCounts in the backend. */
export interface ActivityCounts {
  imported: number;
  /** Already imported (e.g. from files), possible duplicates, or without a GPS track. */
  skipped: number;
  failed: number;
}

/** A connection to an activity service; activitysync.Status in the backend. Never holds the key. */
export interface ConnectionStatus {
  provider: 'garmin';
  status: 'active' | 'invalid_key' | 'key_expired' | 'error';
  /** What went wrong last, written for the user. */
  lastError: string | null;
  keyExpiresAt: string | null;
  connectedAt: string;
  lastSyncedAt: string | null;
  nextSyncAt: string;
  /** Every activity handled since connecting, by the backfill and by polling. */
  activities: ActivityCounts;
  backfill: BackfillStatus;
}

/** The signed-in user; store.User in the backend. */
export interface User {
  id: string;
  email: string;
  displayName: string;
}

let unauthorizedHandler: (() => void) | null = null;

/**
 * Sets what happens when a request is refused because the session is
 * missing or has expired, e.g. showing the sign-in screen.
 */
export function setUnauthorizedHandler(handler: (() => void) | null) {
  unauthorizedHandler = handler;
}

/**
 * The error for a failed response, e.g. "Import failed: the database is
 * unavailable; try again later (500)". The backend's errors are plain text;
 * other bodies, such as a proxy's HTML page, are left out.
 */
async function responseError(res: Response, action: string): Promise<Error> {
  if (res.status === 401) unauthorizedHandler?.();
  const isText = res.headers.get('Content-Type')?.startsWith('text/plain') ?? false;
  const message = isText ? (await res.text()).trim() : '';
  return new Error(message ? `${action} failed: ${message} (${res.status})` : `${action} failed (${res.status})`);
}

/** Fetches JSON from url. what names the data in the error message. */
async function getJSON<T>(url: string, what: string, signal?: AbortSignal): Promise<T> {
  const res = await fetch(url, { signal });
  if (!res.ok) throw await responseError(res, `Loading ${what}`);
  return res.json() as Promise<T>;
}

export function fetchActivities(signal?: AbortSignal): Promise<Activity[]> {
  return getJSON('/api/activities', 'activities', signal);
}

export function fetchConfig(signal?: AbortSignal): Promise<AppConfig> {
  return getJSON('/api/config', 'configuration', signal);
}

/**
 * Uploads files for import. Results are returned in the same order as files.
 * With allowDuplicates, possible duplicates are imported instead of reported.
 */
export async function importFiles(files: File[], allowDuplicates = false): Promise<ImportResult[]> {
  const body = new FormData();
  for (const f of files) body.append('files', f, f.name);
  const url = allowDuplicates ? '/api/activities/import?allowDuplicates=true' : '/api/activities/import';
  const res = await fetch(url, { method: 'POST', body });
  if (!res.ok) throw await responseError(res, 'Import');
  return res.json();
}

/** Deletes one of the user's activities. */
export async function deleteActivity(id: string): Promise<void> {
  const res = await fetch(`/api/activities/${encodeURIComponent(id)}`, { method: 'DELETE' });
  if (!res.ok) throw await responseError(res, 'Delete');
}

/**
 * The error for a failed sign-up or sign-in request. The backend's messages
 * for these are written for users, so they are shown as they are.
 */
async function authError(res: Response): Promise<Error> {
  const isText = res.headers.get('Content-Type')?.startsWith('text/plain') ?? false;
  const message = isText ? (await res.text()).trim() : '';
  if (!message) return new Error(`Something went wrong (${res.status}); try again later.`);
  // Go error strings start in lowercase; these stand on their own.
  return new Error(message.charAt(0).toUpperCase() + message.slice(1));
}

async function postAuth(path: string, body?: unknown): Promise<Response> {
  const res = await fetch(path, {
    method: 'POST',
    headers: body === undefined ? undefined : { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (!res.ok) throw await authError(res);
  return res;
}

/** Returns the signed-in user, or null if there is no valid session. */
export async function fetchMe(signal?: AbortSignal): Promise<User | null> {
  const res = await fetch('/api/me', { signal });
  if (res.status === 401) return null;
  if (!res.ok) throw await responseError(res, 'Loading your account');
  return res.json();
}

/** Signs up. The backend answers the same whether or not the address is new. */
export async function register(email: string, password: string): Promise<void> {
  await postAuth('/api/auth/register', { email, password });
}

/** Confirms an account with the token from the emailed link, and signs in. */
export async function verify(token: string, password: string): Promise<User> {
  return (await postAuth('/api/auth/verify', { token, password })).json();
}

export async function login(email: string, password: string): Promise<User> {
  return (await postAuth('/api/auth/login', { email, password })).json();
}

export async function logout(): Promise<void> {
  await postAuth('/api/auth/logout');
}

/**
 * Sends a request to the connections API. Its error messages are written
 * for users, so they are shown as they are.
 */
async function connectionRequest(method: string, path: string, body?: unknown): Promise<Response> {
  const res = await fetch(path, {
    method,
    headers: body === undefined ? undefined : { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (res.status === 401) unauthorizedHandler?.();
  if (!res.ok) throw await authError(res);
  return res;
}

/** The user's connections to activity services. */
export function fetchConnections(signal?: AbortSignal): Promise<ConnectionStatus[]> {
  return getJSON('/api/connections', 'connections', signal);
}

/**
 * Connects the user's Garmin account with a personal API key, or replaces the
 * key. keyExpiresAt is a date like "2027-11-08", or "" if not given.
 */
export async function connectGarmin(apiKey: string, keyExpiresAt: string): Promise<ConnectionStatus> {
  return (await connectionRequest('PUT', '/api/connections/garmin', { apiKey, keyExpiresAt })).json();
}

/** Disconnects Garmin Connect. Imported activities stay. */
export async function disconnectGarmin(): Promise<void> {
  await connectionRequest('DELETE', '/api/connections/garmin');
}

/** Starts importing everything from Garmin Connect, which the user has agreed to. */
export async function startGarminBackfill(): Promise<ConnectionStatus> {
  return (await connectionRequest('POST', '/api/connections/garmin/backfill', { consent: true })).json();
}

/** Stops importing everything from Garmin Connect. */
export async function cancelGarminBackfill(): Promise<ConnectionStatus> {
  return (await connectionRequest('DELETE', '/api/connections/garmin/backfill')).json();
}
