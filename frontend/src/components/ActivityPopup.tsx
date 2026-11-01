import type { Activity } from '../api';
import { formatDateTime, formatDistance, formatDuration, sportColor, sportLabel } from '../format';

interface Props {
  activity: Activity;
  /** Where activity is among the clicked ones, oldest first. Steppers are shown when there are several. */
  position?: { index: number; count: number };
  onPrev?: () => void;
  onNext?: () => void;
}

/** What the map popup shows about a clicked activity. */
export default function ActivityPopup({ activity, position, onPrev, onNext }: Props) {
  return (
    <div className="activity-popup">
      {position && position.count > 1 && (
        <div className="popup-nav">
          <button type="button" aria-label="Previous activity" onClick={onPrev} disabled={position.index === 0}>
            &lt;
          </button>
          <span className="popup-nav-count">
            {position.index + 1} / {position.count}
          </span>
          <button
            type="button"
            aria-label="Next activity"
            onClick={onNext}
            disabled={position.index === position.count - 1}
          >
            &gt;
          </button>
        </div>
      )}
      <strong>{activity.name}</strong>
      <dl className="popup-facts">
        <dt>Sport</dt>
        <dd className="popup-sport">
          <span className="swatch" style={{ background: sportColor(activity.sport) }} aria-hidden />
          {sportLabel(activity.sport)}
        </dd>
        <dt>Started</dt>
        <dd>{formatDateTime(activity.startedAt)}</dd>
        <dt>Distance</dt>
        <dd>{formatDistance(activity.distanceM)}</dd>
        <dt>Duration</dt>
        <dd>{activity.durationS != null ? formatDuration(activity.durationS) : 'Unknown'}</dd>
      </dl>
      <details className="popup-details">
        <summary>More details</summary>
        <dl className="popup-facts">
          {activity.sourceFilename != null && (
            <>
              <dt>File</dt>
              <dd className="popup-filename">{activity.sourceFilename}</dd>
            </>
          )}
          <dt>Imported</dt>
          <dd>{formatDateTime(activity.importedAt)}</dd>
        </dl>
      </details>
    </div>
  );
}
