import { useEffect, useRef, useState, type FormEvent } from 'react';
import {
  cancelGarminBackfill,
  connectGarmin,
  disconnectGarmin,
  fetchConnections,
  type ConnectionStatus,
} from '../api';
import { formatDate, formatDateTime, formatError } from '../format';
import { formatCount } from '../plural';
import BackfillConsent from './BackfillConsent';

/** Where Garmin Connect users create a personal API key. */
const PERSONAL_API_KEY_URL = 'https://connect.garmin.com/app/settings/personalApiToken';
/** Where Garmin account holders request an export of all their data. */
const EXPORT_URL = 'https://www.garmin.com/account/datamanagement/exportdata/';

/** How often the status is refreshed while a backfill runs, and otherwise. */
const POLL_BACKFILL_MS = 10_000;
const POLL_IDLE_MS = 60_000;
/** Warn this long before the key expires. */
const EXPIRY_WARNING_MS = 14 * 24 * 60 * 60 * 1000;

interface Props {
  /** Called when the sync has added activities, to refresh the list and map. */
  onSynced: () => void;
}

/**
 * Connects the user's Garmin Connect account with a personal API key, asks
 * before importing everything already there, and shows how the sync is going.
 */
export default function GarminConnect({ onSynced }: Props) {
  // undefined while loading, null when not connected.
  const [connection, setConnection] = useState<ConnectionStatus | null | undefined>(undefined);
  const [now, setNow] = useState(() => Date.now());
  const [loadError, setLoadError] = useState<string | null>(null);
  // Bumped to reload the status right away, e.g. after starting a backfill.
  const [reloadKey, setReloadKey] = useState(0);
  const [showSetup, setShowSetup] = useState(false);
  const [showConsent, setShowConsent] = useState(false);
  const [confirmDisconnect, setConfirmDisconnect] = useState(false);
  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);
  // What the sync had done when last seen, to notice new activities.
  const seenRef = useRef<string | null>(null);

  useEffect(() => {
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout> | undefined;
    async function load() {
      let delay = POLL_IDLE_MS;
      try {
        const list = await fetchConnections(controller.signal);
        const garmin = list.find((c) => c.provider === 'garmin') ?? null;
        setConnection(garmin);
        setNow(Date.now());
        setLoadError(null);
        if (garmin?.backfill.status === 'running') delay = POLL_BACKFILL_MS;
      } catch (e: unknown) {
        if (controller.signal.aborted) return;
        setLoadError(formatError(e));
      }
      timer = setTimeout(() => void load(), delay);
    }
    void load();
    return () => {
      controller.abort();
      clearTimeout(timer);
    };
  }, [reloadKey]);

  useEffect(() => {
    if (!connection) return;
    const seen = `${connection.backfill.imported}|${connection.lastSyncedAt}`;
    if (seenRef.current !== null && seenRef.current !== seen) onSynced();
    seenRef.current = seen;
  }, [connection, onSynced]);

  function update(status: ConnectionStatus) {
    setConnection(status);
    setNow(Date.now());
    setReloadKey((k) => k + 1);
  }

  async function run(action: () => Promise<void>) {
    setBusy(true);
    setActionError(null);
    try {
      await action();
    } catch (e: unknown) {
      setActionError(formatError(e));
    } finally {
      setBusy(false);
    }
  }

  if (connection === undefined) {
    return loadError ? <p className="error">{loadError}</p> : null;
  }

  if (connection === null) {
    if (!showSetup) {
      return (
        <div className="garmin-entry">
          <button type="button" className="secondary" onClick={() => setShowSetup(true)}>
            Garmin Connect
          </button>
          <span className="hint">Sync your activities from Garmin automatically</span>
        </div>
      );
    }
    return (
      <section className="garmin" aria-labelledby="garmin-heading">
        <h2 id="garmin-heading">Connect Garmin Connect</h2>
        <KeySteps />
        <KeyForm
          submitLabel="Connect"
          onConnected={(status) => {
            setShowSetup(false);
            update(status);
            if (status.backfill.status === 'not_requested') setShowConsent(true);
          }}
          onCancel={() => setShowSetup(false)}
        />
        <ExportGuide />
      </section>
    );
  }

  const { backfill } = connection;
  const keyProblem = connection.status === 'invalid_key' || connection.status === 'key_expired';
  const expiresSoon =
    connection.keyExpiresAt !== null && Date.parse(connection.keyExpiresAt) - now < EXPIRY_WARNING_MS && !keyProblem;

  return (
    <section className="garmin" aria-labelledby="garmin-heading">
      <h2 id="garmin-heading">Garmin Connect</h2>
      <p className="garmin-status">
        {keyProblem ? (
          <span className="error">Not syncing</span>
        ) : (
          <>
            Connected
            <span className="hint">
              {' · '}
              {connection.lastSyncedAt
                ? `last checked ${formatDateTime(connection.lastSyncedAt)}`
                : 'checking for activities shortly'}
            </span>
          </>
        )}
      </p>

      {connection.lastError && (
        <p className={keyProblem ? 'garmin-message error-box' : 'garmin-message warning-box'} role="status">
          {connection.lastError}
        </p>
      )}
      {expiresSoon && (
        <p className="garmin-message warning-box" role="status">
          Your Garmin API key expires on {formatDate(connection.keyExpiresAt)}. Create a new one in Garmin Connect and
          replace it here to keep syncing.
        </p>
      )}

      <BackfillProgress
        connection={connection}
        syncing={!keyProblem}
        busy={busy}
        onStart={() => setShowConsent(true)}
        onCancel={() => void run(async () => update(await cancelGarminBackfill()))}
      />

      {(showSetup || keyProblem) && (
        <>
          <KeySteps />
          <KeyForm
            submitLabel="Replace key"
            onConnected={(status) => {
              setShowSetup(false);
              update(status);
            }}
            onCancel={keyProblem ? undefined : () => setShowSetup(false)}
          />
        </>
      )}

      {actionError && <p className="error">{actionError}</p>}

      {confirmDisconnect ? (
        <div className="garmin-confirm" role="group" aria-label="Disconnect Garmin Connect">
          <p>Disconnect Garmin Connect? Your key is deleted and syncing stops. Imported activities stay.</p>
          <div className="confirm-actions">
            <button type="button" className="secondary" onClick={() => setConfirmDisconnect(false)} disabled={busy}>
              Cancel
            </button>
            <button
              type="button"
              className="danger"
              disabled={busy}
              onClick={() =>
                void run(async () => {
                  await disconnectGarmin();
                  setConfirmDisconnect(false);
                  setConnection(null);
                  seenRef.current = null;
                })
              }
            >
              {busy ? 'Disconnecting…' : 'Disconnect'}
            </button>
          </div>
        </div>
      ) : (
        <div className="garmin-actions">
          {!showSetup && !keyProblem && (
            <button type="button" className="link" onClick={() => setShowSetup(true)}>
              Replace key
            </button>
          )}
          <button type="button" className="link" onClick={() => setConfirmDisconnect(true)}>
            Disconnect
          </button>
        </div>
      )}

      {showConsent && (
        <BackfillConsent
          onCancel={() => setShowConsent(false)}
          onStarted={(status) => {
            setShowConsent(false);
            update(status);
          }}
        />
      )}
      {backfill.status === 'not_requested' && <ExportGuide />}
    </section>
  );
}

interface BackfillProgressProps {
  connection: ConnectionStatus;
  /** False while the key is rejected: nothing is synced until it is replaced. */
  syncing: boolean;
  busy: boolean;
  onStart: () => void;
  onCancel: () => void;
}

/** What has come of importing everything from Garmin, and the buttons for it. */
function BackfillProgress({ connection, syncing, busy, onStart, onCancel }: BackfillProgressProps) {
  const b = connection.backfill;
  const automatic = syncing ? ' New activities are added automatically.' : '';
  const counts = [
    `${formatCount(b.imported, 'activity', 'activities')} imported`,
    b.skipped > 0 && `${b.skipped} skipped`,
    b.failed > 0 && `${b.failed} failed`,
  ]
    .filter(Boolean)
    .join(', ');

  switch (b.status) {
    case 'not_requested':
      return (
        <div className="garmin-backfill">
          <p className="hint">{automatic} Your earlier activities aren't imported yet.</p>
          {syncing && (
            <button type="button" className="primary" onClick={onStart}>
              Import all past activities
            </button>
          )}
        </div>
      );
    case 'running':
      return (
        <div className="garmin-backfill" role="status">
          <p>
            {syncing ? 'Importing your Garmin history…' : 'Importing your Garmin history is paused.'}
            <span className="hint"> {b.checked} checked so far: {counts}.</span>
          </p>
          {syncing && <progress aria-label="Importing your Garmin history" />}
          <button type="button" className="secondary" onClick={onCancel} disabled={busy}>
            {busy ? 'Stopping…' : 'Stop import'}
          </button>
        </div>
      );
    case 'done':
      return (
        <p className="garmin-backfill hint">
          Your Garmin history is imported: {counts}.{automatic}
        </p>
      );
    case 'cancelled':
    case 'failed':
      return (
        <div className="garmin-backfill">
          <p className="hint">
            Importing your history {b.status === 'cancelled' ? 'was stopped' : 'failed'} after {b.checked} activities:{' '}
            {counts}.{automatic}
          </p>
          {syncing && (
            <button type="button" className="secondary" onClick={onStart}>
              Continue importing
            </button>
          )}
        </div>
      );
  }
}

/** How to create a personal API key in Garmin Connect. */
function KeySteps() {
  return (
    <ol className="garmin-steps">
      <li>
        Open{' '}
        <a href={PERSONAL_API_KEY_URL} target="_blank" rel="noopener noreferrer">
          Personal API key in Garmin Connect
          <span aria-hidden="true"> ↗</span>
          <span className="visually-hidden"> (opens in a new tab)</span>
        </a>{' '}
        and sign in if asked.
      </li>
      <li>Create a key. Choose the longest expiry, one year, so you don't have to do this again soon.</li>
      <li>Copy the key and paste it below. Garmin only shows it once.</li>
    </ol>
  );
}

interface KeyFormProps {
  submitLabel: string;
  onConnected: (status: ConnectionStatus) => void;
  onCancel?: () => void;
}

/** Asks for a personal API key and connects with it. */
function KeyForm({ submitLabel, onConnected, onCancel }: KeyFormProps) {
  const [apiKey, setApiKey] = useState('');
  const [expires, setExpires] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const status = await connectGarmin(apiKey, expires);
      setApiKey('');
      onConnected(status);
    } catch (err: unknown) {
      setError(formatError(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="garmin-form" onSubmit={(e) => void handleSubmit(e)}>
      <label>
        Personal API key
        <input
          type="password"
          value={apiKey}
          onChange={(e) => setApiKey(e.target.value)}
          autoComplete="off"
          spellCheck={false}
          required
        />
      </label>
      <label>
        <span>
          Key expires on <span className="hint">(optional, to be reminded)</span>
        </span>
        <input type="date" value={expires} onChange={(e) => setExpires(e.target.value)} />
      </label>
      <p className="hint">
        The key is checked with Garmin, then stored encrypted and only used to read your activities.
      </p>
      {error && <p className="error">{error}</p>}
      <div className="confirm-actions">
        {onCancel && (
          <button type="button" className="secondary" onClick={onCancel} disabled={busy}>
            Cancel
          </button>
        )}
        <button type="submit" className="primary" disabled={busy || apiKey.trim() === ''}>
          {busy ? 'Checking with Garmin…' : submitLabel}
        </button>
      </div>
    </form>
  );
}

/** The other way to get everything from Garmin: its data export. */
function ExportGuide() {
  return (
    <details className="rules">
      <summary>Or import a Garmin data export instead</summary>
      <ol className="garmin-steps">
        <li>
          Request an{' '}
          <a href={EXPORT_URL} target="_blank" rel="noopener noreferrer">
            export of your Garmin data
            <span aria-hidden="true"> ↗</span>
            <span className="visually-hidden"> (opens in a new tab)</span>
          </a>
          . Garmin emails you a download link, which can take a few days.
        </li>
        <li>
          Unzip the download, then the zip files in <code>DI_CONNECT/DI-Connect-Uploaded-Files</code>.
        </li>
        <li>Drop the .fit files on Import above. Activities you already have are skipped.</li>
      </ol>
    </details>
  );
}
