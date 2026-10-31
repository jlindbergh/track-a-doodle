import type { Activity } from '../api';
import { formatDateTime, formatDistance, formatDuration, sportColor, sportLabel } from '../format';

interface Props {
  activity: Activity;
}

/** What the map popup shows about a clicked activity. */
export default function ActivityPopup({ activity }: Props) {
  return (
    <div className="activity-popup">
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
