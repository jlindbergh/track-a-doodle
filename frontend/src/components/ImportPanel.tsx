import { useRef, useState, type ChangeEvent, type DragEvent } from 'react';
import { importFiles, type ImportResult } from '../api';
import { useConfig } from '../config';
import DuplicateReview, { type ReviewItem } from './DuplicateReview';
import DuplicateRulesInfo from './DuplicateRulesInfo';
import FailedImports, { type FailedItem } from './FailedImports';
import NoTrackNotice, { type NoTrackItem } from './NoTrackNotice';
import { formatError } from '../format';
import { formatCount } from '../plural';

interface Props {
  /** Called after an import that stored at least one new activity. */
  onImported: () => void;
}

/** One uploaded file and what happened to it. */
interface Entry {
  key: string;
  file: File;
  result: ImportResult;
}

let nextKey = 0;

function possibleDuplicates(n: number): string {
  return formatCount(n, 'possible duplicate', 'possible duplicates');
}

export default function ImportPanel({ onImported }: Props) {
  const inputRef = useRef<HTMLInputElement>(null);
  const extensions = useConfig().import.acceptedExtensions;
  const [busy, setBusy] = useState(false);
  const [dragging, setDragging] = useState(false);
  const [entries, setEntries] = useState<Entry[] | null>(null);
  // Possible duplicates the user has looked at and decided to skip.
  const [skipped, setSkipped] = useState<ReadonlySet<string>>(new Set());
  const [selected, setSelected] = useState<ReadonlySet<string>>(new Set());
  const [error, setError] = useState<string | null>(null);

  /** Uploads files and hands the results, in upload order, to onResults. */
  async function runImport(files: File[], allowDuplicates: boolean, onResults: (results: ImportResult[]) => void) {
    setBusy(true);
    setError(null);
    try {
      const results = await importFiles(files, allowDuplicates);
      onResults(results);
      if (results.some((r) => r.status === 'imported')) onImported();
    } catch (e) {
      setError(formatError(e));
    } finally {
      setBusy(false);
    }
  }

  async function upload(files: File[]) {
    const accepted = files.filter((f) => extensions.some((ext) => f.name.toLowerCase().endsWith(ext)));
    if (accepted.length === 0) {
      setError(`Only ${extensions.join(', ')} files can be imported for now.`);
      return;
    }
    setEntries(null);
    setSkipped(new Set());
    setSelected(new Set());
    await runImport(accepted, false, (results) => {
      setEntries(results.map((result, i) => ({ key: String(nextKey++), file: accepted[i], result })));
    });
  }

  async function importSelectedAnyway() {
    if (!entries) return;
    const chosen = entries.filter((e) => selected.has(e.key));
    await runImport(
      chosen.map((e) => e.file),
      true,
      (results) => {
        const byKey = new Map(chosen.map((e, i) => [e.key, results[i]]));
        setEntries(entries.map((e) => ({ ...e, result: byKey.get(e.key) ?? e.result })));
        setSelected(new Set());
      },
    );
  }

  function toggle(key: string) {
    const next = new Set(selected);
    if (next.has(key)) next.delete(key);
    else next.add(key);
    setSelected(next);
  }

  function skipAll() {
    setSkipped(new Set([...skipped, ...review.map((r) => r.key)]));
    setSelected(new Set());
  }

  function handleChange(e: ChangeEvent<HTMLInputElement>) {
    const files = Array.from(e.target.files ?? []);
    e.target.value = '';
    void upload(files);
  }

  function handleDrop(e: DragEvent<HTMLDivElement>) {
    e.preventDefault();
    setDragging(false);
    void upload(Array.from(e.dataTransfer.files));
  }

  const results = entries?.map((e) => e.result) ?? [];
  const count = (status: ImportResult['status']) => results.filter((r) => r.status === status).length;
  const failed: FailedItem[] = (entries ?? [])
    .filter((e) => e.result.status === 'failed')
    .map((e) => ({ key: e.key, file: e.file.name, error: e.result.error ?? 'Unknown error' }));
  const review: ReviewItem[] = (entries ?? [])
    .filter((e) => e.result.status === 'possible_duplicate' && !skipped.has(e.key))
    .flatMap(({ key, file, result: { activity, existing, comparison } }) =>
      activity && existing && comparison ? [{ key, file: file.name, activity, existing, comparison }] : [],
    );
  const noTrack: NoTrackItem[] = (entries ?? [])
    .filter((e) => e.result.status === 'no_gps_track')
    .map((e) => ({
      key: e.key,
      file: e.file.name,
      name: e.result.activity?.name ?? '',
      sport: e.result.activity?.sport ?? 'unknown',
    }));

  const summary = entries && [
    `${count('imported')} imported`,
    count('already_imported') > 0 && `${count('already_imported')} already imported (exact copies)`,
    review.length > 0 && `${possibleDuplicates(review.length)} to review`,
    skipped.size > 0 && `${possibleDuplicates(skipped.size)} skipped`,
    noTrack.length > 0 && `${noTrack.length} without GPS track`,
    failed.length > 0 && `${failed.length} failed`,
  ];

  return (
    <div className="import-panel">
      <div
        className={dragging ? 'import dragging' : 'import'}
        onDragOver={(e) => {
          e.preventDefault();
          setDragging(true);
        }}
        onDragLeave={() => setDragging(false)}
        onDrop={handleDrop}
      >
        <input ref={inputRef} type="file" accept={extensions.join(',')} multiple hidden onChange={handleChange} />
        <button type="button" className="primary" disabled={busy} onClick={() => inputRef.current?.click()}>
          {busy ? 'Importing…' : 'Import files'}
        </button>
        <span className="hint">
          {extensions.map((ext) => ext.slice(1).toUpperCase()).join(' or ')} files, or drop them here
        </span>
        <DuplicateRulesInfo />

        {error && <p className="error">{error}</p>}
        {summary && <div className="import-result">{summary.filter(Boolean).join(', ')}</div>}
      </div>

      {failed.length > 0 && <FailedImports items={failed} />}

      {noTrack.length > 0 && <NoTrackNotice items={noTrack} />}

      {review.length > 0 && (
        <DuplicateReview
          items={review}
          selected={selected}
          busy={busy}
          onToggle={toggle}
          onImportSelected={() => void importSelectedAnyway()}
          onSkipAll={skipAll}
        />
      )}
    </div>
  );
}
