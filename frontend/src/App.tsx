import { useEffect, useRef, useState } from 'react';
import type { MapRef } from 'react-map-gl/maplibre';
import { fetchActivities, type Activity } from './api';
import { unionBBox, type BBox } from './bbox';
import ActivityList, { type ActivityListHandle } from './components/ActivityList';
import ConfirmDelete from './components/ConfirmDelete';
import ImportPanel from './components/ImportPanel';
import TrackMap, { type TrackMapHandle } from './components/TrackMap';
import { formatError } from './format';

export default function App() {
  const mapRef = useRef<MapRef>(null);
  const listRef = useRef<ActivityListHandle>(null);
  const trackMapRef = useRef<TrackMapHandle>(null);
  const [activities, setActivities] = useState<Activity[]>([]);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  // Bumped after each import: refetches the list and fits the map to it.
  const [dataVersion, setDataVersion] = useState(0);
  // Bumped whenever the user's data changes: refetches the map tiles.
  const [tilesVersion, setTilesVersion] = useState(0);
  // The activity the delete confirmation is shown for.
  const [pendingDelete, setPendingDelete] = useState<Activity | null>(null);
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

  // From the list. The map moves to the activity, likely away from an open popup, so it's closed.
  function handleSelect(activity: Activity) {
    trackMapRef.current?.closePopup();
    setSelectedId(activity.id);
    fitBBox(activity.bbox);
  }

  function handleImported() {
    setDataVersion((v) => v + 1);
    setTilesVersion((v) => v + 1);
  }

  // Drops the activity locally rather than refetching, so the map stays where it is.
  function handleDeleted(id: string) {
    setPendingDelete(null);
    setActivities((list) => list.filter((a) => a.id !== id));
    if (selectedId === id) setSelectedId(null);
    setTilesVersion((v) => v + 1);
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
        <ImportPanel onImported={handleImported} />
        {loadError && <p className="error">{loadError}</p>}
        <ActivityList
          ref={listRef}
          activities={activities}
          selectedId={selectedId}
          onSelect={handleSelect}
          onRequestDelete={setPendingDelete}
        />
      </aside>
      <main className="map">
        <TrackMap
          ref={trackMapRef}
          mapRef={mapRef}
          activities={activities}
          dataVersion={tilesVersion}
          selectedId={selectedId}
          onSelect={setSelectedId}
          onShowInList={(id) => listRef.current?.show(id)}
          onFitBounds={fitBBox}
          onLoad={() => setMapReady(true)}
        />
      </main>
      {pendingDelete && (
        <ConfirmDelete
          key={pendingDelete.id}
          activity={pendingDelete}
          onCancel={() => setPendingDelete(null)}
          onDeleted={handleDeleted}
        />
      )}
    </div>
  );
}
