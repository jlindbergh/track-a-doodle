import { useEffect, useRef, useState } from 'react';
import type { MapRef } from 'react-map-gl/maplibre';
import { fetchActivities, type Activity } from './api';
import ActivityList from './components/ActivityList';
import ImportPanel from './components/ImportPanel';
import TrackMap from './components/TrackMap';
import { formatError } from './format';

type BBox = Activity['bbox'];

function unionBBox(activities: Activity[]): BBox | null {
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

export default function App() {
  const mapRef = useRef<MapRef>(null);
  const [activities, setActivities] = useState<Activity[]>([]);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  // Bumped after each import: refetches the list and the map tiles.
  const [dataVersion, setDataVersion] = useState(0);
  const [mapReady, setMapReady] = useState(false);
  // Where to move the map once it is ready, e.g. all tracks after loading.
  const [pendingFit, setPendingFit] = useState<BBox | null>(null);

  useEffect(() => {
    const controller = new AbortController();
    fetchActivities(controller.signal)
      .then((list) => {
        setActivities(list);
        setLoadError(null);
        setPendingFit(unionBBox(list));
      })
      .catch((e: unknown) => {
        if (!controller.signal.aborted) setLoadError(formatError(e));
      });
    return () => controller.abort();
  }, [dataVersion]);

  useEffect(() => {
    if (mapReady && pendingFit) {
      fitBBox(pendingFit);
      setPendingFit(null);
    }
  }, [mapReady, pendingFit]);

  function fitBBox(bbox: BBox) {
    mapRef.current?.fitBounds(
      [
        [bbox[0], bbox[1]],
        [bbox[2], bbox[3]],
      ],
      { padding: 60, maxZoom: 15, duration: 800 },
    );
  }

  function handleSelect(activity: Activity) {
    setSelectedId(activity.id);
    fitBBox(activity.bbox);
  }

  return (
    <div className="app">
      <aside className="sidebar">
        <header>
          <h1>Track-a-doodle</h1>
          {activities.length > 0 && (
            <button type="button" className="link" onClick={() => fitBBox(unionBBox(activities)!)}>
              Show all ({activities.length})
            </button>
          )}
        </header>
        <ImportPanel onImported={() => setDataVersion((v) => v + 1)} />
        {loadError && <p className="error">{loadError}</p>}
        <ActivityList activities={activities} selectedId={selectedId} onSelect={handleSelect} />
      </aside>
      <main className="map">
        <TrackMap
          mapRef={mapRef}
          activities={activities}
          dataVersion={dataVersion}
          selectedId={selectedId}
          onSelect={setSelectedId}
          onLoad={() => setMapReady(true)}
        />
      </main>
    </div>
  );
}
