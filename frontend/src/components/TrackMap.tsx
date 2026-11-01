import type { FilterSpecification } from 'maplibre-gl';
import { useImperativeHandle, useState, type Ref, type RefObject } from 'react';
import Map, {
  Layer,
  NavigationControl,
  Popup,
  Source,
  type LayerProps,
  type MapLayerMouseEvent,
  type MapRef,
} from 'react-map-gl/maplibre';
import type { Activity } from '../api';
import { useConfig } from '../config';
import { defaultSportColor, sportStyles } from '../format';
import ActivityPopup from './ActivityPopup';

// OpenFreeMap: free vector basemap, no API key. Data © OpenStreetMap
// contributors (ODbL); attribution is supplied by the style itself.
const basemapStyle = 'https://tiles.openfreemap.org/styles/liberty';

const lineColor = [
  'match',
  ['get', 'sport'],
  ...Object.entries(sportStyles).flatMap(([sport, style]) => [sport, style.color]),
  defaultSportColor,
] as unknown as string;

const lineWidth = ['interpolate', ['linear'], ['zoom'], 5, 1.5, 12, 3, 16, 5] as unknown as number;

// The dark outline around the selected activity, a bit wider than lineWidth at every zoom.
const selectedCasingWidth = ['interpolate', ['linear'], ['zoom'], 5, 5, 12, 7.5, 16, 9.5] as unknown as number;

const lineLayout = { 'line-cap': 'round', 'line-join': 'round' } as const;

const activitiesLayerId = 'activities-line';

const activitiesLayer: LayerProps = {
  id: activitiesLayerId,
  type: 'line',
  layout: lineLayout,
  paint: { 'line-color': lineColor, 'line-width': lineWidth, 'line-opacity': 0.8 },
};

// How far from a click, in pixels, a track still counts as clicked.
const clickTolerancePx = 4;

interface PopupInfo {
  longitude: number;
  latitude: number;
  /** The clicked activities, oldest first: the same order the tiles draw them in. */
  activityIds: string[];
  /** The one shown in the popup. */
  index: number;
}

/** Oldest first, undated first; matches the ORDER BY in postgis/init/030_tiles.sql. */
function compareByStart(a: Activity, b: Activity): number {
  if (a.startedAt !== b.startedAt) {
    if (a.startedAt == null) return -1;
    if (b.startedAt == null) return 1;
    return Date.parse(a.startedAt) - Date.parse(b.startedAt);
  }
  return a.id < b.id ? -1 : a.id > b.id ? 1 : 0;
}

/** What the map lets its parent do, through its ref. */
export interface TrackMapHandle {
  closePopup(): void;
}

interface Props {
  ref?: Ref<TrackMapHandle>;
  mapRef: RefObject<MapRef | null>;
  /** The user's activities. The popup shows the clicked one; tiles only hold ids and sports. */
  activities: Activity[];
  /** Changes whenever the user's data changes, so tiles are refetched. */
  dataVersion: number;
  selectedId: string | null;
  onSelect: (id: string | null) => void;
  /** Scrolls the sidebar list to the activity with this id. */
  onShowInList: (id: string) => void;
  onLoad: () => void;
}

export default function TrackMap({
  ref,
  mapRef,
  activities,
  dataVersion,
  selectedId,
  onSelect,
  onShowInList,
  onLoad,
}: Props) {
  const { tiles } = useConfig();
  const [popup, setPopup] = useState<PopupInfo | null>(null);
  const [cursor, setCursor] = useState<string>('');

  useImperativeHandle(ref, () => ({
    closePopup() {
      setPopup(null);
    },
  }));

  function handleClick(e: MapLayerMouseEvent) {
    const { x, y } = e.point;
    const features =
      mapRef.current?.queryRenderedFeatures(
        [
          [x - clickTolerancePx, y - clickTolerancePx],
          [x + clickTolerancePx, y + clickTolerancePx],
        ],
        { layers: [activitiesLayerId] },
      ) ?? [];
    // A track can come back once per tile it crosses.
    const ids = new Set(features.map((f) => String(f.properties.id)));
    const hits = activities.filter((a) => ids.has(a.id)).sort(compareByStart);
    if (hits.length === 0) {
      setPopup(null);
      onSelect(null);
      return;
    }
    // Start with the most recent, which is the one drawn on top.
    const index = hits.length - 1;
    setPopup({ longitude: e.lngLat.lng, latitude: e.lngLat.lat, activityIds: hits.map((a) => a.id), index });
    onSelect(hits[index].id);
  }

  function step(delta: number) {
    if (!popup) return;
    const index = Math.min(Math.max(popup.index + delta, 0), popup.activityIds.length - 1);
    setPopup({ ...popup, index });
    onSelect(popup.activityIds[index]);
  }

  // The popup is hidden once any of its activities is deleted, rather than stepping over a gap.
  const popupStale = popup != null && !popup.activityIds.every((id) => activities.some((a) => a.id === id));
  const popupActivity = popup && !popupStale && activities.find((a) => a.id === popup.activityIds[popup.index]);
  const selectedFilter: FilterSpecification = ['==', ['get', 'id'], selectedId ?? ''];

  return (
    <Map
      ref={mapRef}
      initialViewState={{ longitude: 15, latitude: 62, zoom: 4 }}
      mapStyle={basemapStyle}
      onLoad={onLoad}
      interactiveLayerIds={[activitiesLayerId]}
      onClick={handleClick}
      onMouseEnter={() => setCursor('pointer')}
      onMouseLeave={() => setCursor('')}
      cursor={cursor}
    >
      <NavigationControl position="top-right" />
      {/* Keyed on dataVersion so new imports show up without a page reload. */}
      <Source
        key={dataVersion}
        id="activities"
        type="vector"
        // MapLibre needs an absolute URL. Not new URL(): it would escape the {z}/{x}/{y} braces.
        tiles={[`${window.location.origin}${tiles.url}`]}
        minzoom={tiles.minZoom}
        maxzoom={tiles.maxZoom}
      >
        <Layer {...activitiesLayer} source-layer={tiles.sourceLayer} />
        {/* Declared after the activities layer, so the selected activity is drawn on top. */}
        <Layer
          id="activities-selected-casing"
          type="line"
          source-layer={tiles.sourceLayer}
          filter={selectedFilter}
          layout={lineLayout}
          paint={{ 'line-color': '#111', 'line-width': selectedCasingWidth, 'line-opacity': 0.9 }}
        />
        <Layer
          id="activities-selected-line"
          type="line"
          source-layer={tiles.sourceLayer}
          filter={selectedFilter}
          layout={lineLayout}
          paint={{ 'line-color': lineColor, 'line-width': lineWidth }}
        />
      </Source>
      {popup && popupActivity && (
        <Popup
          longitude={popup.longitude}
          latitude={popup.latitude}
          onClose={() => setPopup(null)}
          closeOnClick={false}
          maxWidth="300px"
        >
          <ActivityPopup
            activity={popupActivity}
            position={{ index: popup.index, count: popup.activityIds.length }}
            onPrev={() => step(-1)}
            onNext={() => step(1)}
            onShowInList={() => onShowInList(popupActivity.id)}
          />
        </Popup>
      )}
    </Map>
  );
}
