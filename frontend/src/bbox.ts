import type { Activity } from './api';

/** [west, south, east, north] in WGS 84. */
export type BBox = Activity['bbox'];

/** The smallest box around all the activities, or null if there are none. */
export function unionBBox(activities: Activity[]): BBox | null {
  if (activities.length === 0) return null;
  return activities.reduce<BBox>(
    (acc, a) => [
      Math.min(acc[0], a.bbox[0]),
      Math.min(acc[1], a.bbox[1]),
      Math.max(acc[2], a.bbox[2]),
      Math.max(acc[3], a.bbox[3]),
    ],
    activities[0].bbox,
  );
}
