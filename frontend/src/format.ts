/** The message to show for a caught error. */
export function formatError(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}

export function formatDistance(m: number): string {
  return m >= 1000 ? `${(m / 1000).toFixed(1)} km` : `${Math.round(m)} m`;
}

export function formatDuration(s: number | null): string {
  if (s == null) return '';
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  return h > 0 ? `${h} h ${m} min` : `${m} min`;
}

export function formatDate(value: string | null): string {
  if (value == null) return 'Unknown date';
  return new Date(value).toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' });
}

export function formatDateTime(value: string | null): string {
  if (value == null) return 'Unknown time';
  return new Date(value).toLocaleString(undefined, {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  });
}

export function formatPercent(fraction: number): string {
  return `${Number((fraction * 100).toFixed(1))}%`;
}

/**
 * How the sports we have a style for are shown, by sport id. The backend lists
 * the ids it recognizes in GET /api/config; any other sport gets
 * defaultSportColor and a label made from its id.
 */
export const sportStyles: Record<string, { label: string; color: string }> = {
  running: { label: 'Running', color: '#e4572e' },
  cycling: { label: 'Cycling', color: '#2e86ab' },
  walking: { label: 'Walking', color: '#76b041' },
  hiking: { label: 'Hiking', color: '#a1662f' },
  swimming: { label: 'Swimming', color: '#17bebb' },
  cross_country_skiing: { label: 'Cross-country skiing', color: '#b5179e' },
  downhill_skiing: { label: 'Downhill skiing', color: '#f2a900' },
  skating: { label: 'Skating', color: '#1d3557' },
};
export const defaultSportColor = '#7d5ba6';

export function sportColor(sport: string): string {
  return sportStyles[sport]?.color ?? defaultSportColor;
}

/** The sport's label, or one made from its id, e.g. "Virtual ride" for "virtual_ride". */
export function sportLabel(sport: string): string {
  const style = sportStyles[sport];
  if (style) return style.label;
  const words = sport.replace(/_/g, ' ');
  return words.charAt(0).toUpperCase() + words.slice(1);
}
