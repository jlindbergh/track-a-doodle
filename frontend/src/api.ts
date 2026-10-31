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
}

/**
 * The error for a failed response, e.g. "Import failed: the database is
 * unavailable; try again later (500)". The backend's errors are plain text;
 * other bodies, such as a proxy's HTML page, are left out.
 */
async function responseError(res: Response, action: string): Promise<Error> {
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
