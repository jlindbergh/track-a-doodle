import { useEffect, useRef, useState } from 'react';
import { startGarminBackfill, type ConnectionStatus } from '../api';
import { formatError } from '../format';

interface Props {
  onCancel: () => void;
  onStarted: (status: ConnectionStatus) => void;
}

/**
 * Explains what importing everything from Garmin Connect does and asks the
 * user to agree before it starts. Rendered only while it is shown.
 */
export default function BackfillConsent({ onCancel, onStarted }: Props) {
  const dialogRef = useRef<HTMLDialogElement>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    const dialog = dialogRef.current;
    dialog?.showModal();
    return () => dialog?.close();
  }, []);

  async function handleStart() {
    setBusy(true);
    setError(null);
    try {
      onStarted(await startGarminBackfill());
    } catch (e: unknown) {
      setError(formatError(e));
      setBusy(false);
    }
  }

  return (
    <dialog
      ref={dialogRef}
      className="confirm-dialog consent-dialog"
      aria-labelledby="consent-title"
      aria-describedby="consent-what"
      onCancel={(e) => {
        if (busy) e.preventDefault();
      }}
      // See ConfirmDelete: the close event is queued.
      onClose={() => {
        if (!dialogRef.current?.open) onCancel();
      }}
    >
      <h2 id="consent-title">Import all your Garmin activities?</h2>
      <ul className="consent-points">
        <li id="consent-what">
          Track-a-doodle downloads <strong>every activity with a GPS track</strong> in your Garmin Connect account, as
          the original files from your watch, and adds them to your map.
        </li>
        <li>
          Activities without GPS, like gym sessions or treadmill runs, are left out. Activities you have already
          imported from files are recognised and skipped.
        </li>
        <li>
          It can take a while: about an hour per thousand activities. It carries on in the background when you close
          the page, and you can stop it at any time. What has been imported stays.
        </li>
        <li>Only you can see your activities.</li>
      </ul>
      {error && <p className="error">{error}</p>}
      <div className="confirm-actions">
        <button type="button" className="secondary" onClick={onCancel} disabled={busy}>
          Not now
        </button>
        <button type="button" className="primary" onClick={handleStart} disabled={busy}>
          {busy ? 'Starting…' : 'Import all activities'}
        </button>
      </div>
    </dialog>
  );
}
