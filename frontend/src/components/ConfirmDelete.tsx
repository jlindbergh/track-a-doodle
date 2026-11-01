import { useEffect, useRef, useState } from 'react';
import { deleteActivity, type Activity } from '../api';
import { formatDate, formatDistance, formatError, sportLabel } from '../format';

interface Props {
  activity: Activity;
  onCancel: () => void;
  onDeleted: (id: string) => void;
}

/** Asks before deleting activity, then deletes it. Rendered only while a delete is pending. */
export default function ConfirmDelete({ activity, onCancel, onDeleted }: Props) {
  const dialogRef = useRef<HTMLDialogElement>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    const dialog = dialogRef.current;
    // Modal: traps focus, adds a backdrop and closes on Escape.
    dialog?.showModal();
    return () => dialog?.close();
  }, []);

  async function handleDelete() {
    setBusy(true);
    setError(null);
    try {
      await deleteActivity(activity.id);
      onDeleted(activity.id);
    } catch (e: unknown) {
      setError(formatError(e));
      setBusy(false);
    }
  }

  return (
    <dialog
      ref={dialogRef}
      className="confirm-dialog"
      aria-labelledby="confirm-delete-title"
      // Escape: not while the delete is running.
      onCancel={(e) => {
        if (busy) e.preventDefault();
      }}
      // The event is queued, so the dialog may have been opened again since:
      // StrictMode runs the effect's cleanup and setup twice in development.
      onClose={() => {
        if (!dialogRef.current?.open) onCancel();
      }}
    >
      <h2 id="confirm-delete-title">Delete activity?</h2>
      <p className="confirm-activity">
        <strong>{activity.name}</strong>
        <span className="activity-meta">
          {formatDate(activity.startedAt)} · {sportLabel(activity.sport)} · {formatDistance(activity.distanceM)}
        </span>
      </p>
      <p>It's removed from the map and the list. Import the file again to get it back.</p>
      {error && <p className="error">{error}</p>}
      <div className="confirm-actions">
        {/* First, so it has focus when the dialog opens and Enter doesn't delete. */}
        <button type="button" className="secondary" onClick={onCancel} disabled={busy}>
          Cancel
        </button>
        <button type="button" className="danger" onClick={handleDelete} disabled={busy}>
          {busy ? 'Deleting…' : 'Delete'}
        </button>
      </div>
    </dialog>
  );
}
