import { useImperativeHandle, useRef, useState, type MouseEvent, type Ref, type ToggleEvent } from 'react';
import type { Activity } from '../api';
import { formatDate, formatDistance, formatDuration, sportColor, sportLabel } from '../format';

const menuId = 'activity-menu';

/** Gap between a row's actions button and its menu, in pixels. */
const menuGap = 4;

/** How long a row is outlined after show(), in milliseconds. */
const flashDuration = 1200;

/** What the list lets its parent do, through its ref. */
export interface ActivityListHandle {
  /** Scrolls to the activity's row, focuses it and briefly outlines it. */
  show(id: string): void;
}

function rowClass(selected: boolean, menuOpen: boolean): string {
  return ['activity-row', selected && 'selected', menuOpen && 'menu-open'].filter(Boolean).join(' ');
}

interface Props {
  activities: Activity[];
  selectedId: string | null;
  onSelect: (activity: Activity) => void;
  onRequestDelete: (activity: Activity) => void;
  ref?: Ref<ActivityListHandle>;
}

export default function ActivityList({ activities, selectedId, onSelect, onRequestDelete, ref }: Props) {
  const listRef = useRef<HTMLUListElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);
  // The actions button the menu was opened from, to place it after it opens.
  const anchorRef = useRef<HTMLElement | null>(null);
  // The activity whose menu is open, if any.
  const [menuFor, setMenuFor] = useState<Activity | null>(null);

  useImperativeHandle(ref, () => ({
    show(id) {
      const row = listRef.current?.querySelector<HTMLLIElement>(`[data-activity-id="${CSS.escape(id)}"]`);
      if (!row) return;
      // Focus first, without scrolling, so only the scroll below moves the list.
      row.querySelector<HTMLButtonElement>('.activity')?.focus({ preventScroll: true });
      const reducedMotion = window.matchMedia('(prefers-reduced-motion: reduce)').matches;
      row.scrollIntoView({ block: 'center', behavior: reducedMotion ? 'auto' : 'smooth' });
      const accent = getComputedStyle(row).getPropertyValue('--accent');
      row.animate([{ boxShadow: `inset 0 0 0 2px ${accent}` }, { boxShadow: 'inset 0 0 0 2px transparent' }], {
        duration: flashDuration,
        easing: 'ease-out',
      });
    },
  }));

  /** Places the menu below anchor, right-aligned with it. */
  function placeMenu(anchor: HTMLElement) {
    const menu = menuRef.current;
    if (!menu) return;
    const rect = anchor.getBoundingClientRect();
    anchorRef.current = anchor;
    menu.style.right = `${window.innerWidth - rect.right}px`;
    menu.style.top = `${rect.bottom + menuGap}px`;
  }

  function handleMenuToggle(e: ToggleEvent<HTMLDivElement>) {
    const menu = e.currentTarget;
    // Not e.newState: toggle events are queued, and the menu may have been reopened since.
    if (!menu.matches(':popover-open')) {
      setMenuFor(null);
      return;
    }
    // Above the button instead if it doesn't fit below; only measurable once open.
    const anchor = anchorRef.current;
    if (anchor && menu.getBoundingClientRect().bottom > window.innerHeight) {
      menu.style.top = `${anchor.getBoundingClientRect().top - menuGap - menu.offsetHeight}px`;
    }
    menu.querySelector<HTMLElement>('[role="menuitem"]')?.focus();
  }

  // Right-click, or the keyboard's context menu key: the same menu as the actions button.
  function handleContextMenu(e: MouseEvent<HTMLLIElement>, activity: Activity) {
    const menu = menuRef.current;
    const anchor = e.currentTarget.querySelector<HTMLElement>('.activity-actions');
    if (!menu || !anchor) return;
    e.preventDefault();
    const open = () => {
      setMenuFor(activity);
      placeMenu(anchor);
      if (!menu.matches(':popover-open')) menu.showPopover();
    };
    // On macOS this comes while the button is still down, and releasing it would
    // light-dismiss a menu opened now, so open it after the release instead.
    if (e.buttons !== 0) {
      document.addEventListener('pointerup', () => setTimeout(open), { once: true });
    } else {
      open();
    }
  }

  function handleDelete() {
    menuRef.current?.hidePopover();
    if (menuFor) onRequestDelete(menuFor);
  }

  if (activities.length === 0) {
    return <p className="empty">No activities yet. Import some GPX files to get started.</p>;
  }
  return (
    <>
      <ul ref={listRef} className="activity-list">
        {activities.map((a) => (
          <li
            key={a.id}
            data-activity-id={a.id}
            className={rowClass(a.id === selectedId, a.id === menuFor?.id)}
            onContextMenu={(e) => handleContextMenu(e, a)}
          >
            <button type="button" className="activity" onClick={() => onSelect(a)}>
              <span className="swatch" style={{ background: sportColor(a.sport) }} aria-hidden />
              <span className="activity-text">
                <span className="activity-name">{a.name}</span>
                <span className="activity-meta">
                  {formatDate(a.startedAt)} · {sportLabel(a.sport)} · {formatDistance(a.distanceM)}
                  {a.durationS != null && ` · ${formatDuration(a.durationS)}`}
                </span>
              </span>
            </button>
            <button
              type="button"
              className="activity-actions"
              aria-label={`Actions for ${a.name}`}
              aria-haspopup="menu"
              popoverTarget={menuId}
              // Toggles on its own row; from another row, moves the open menu here.
              popoverTargetAction={a.id === menuFor?.id ? 'toggle' : 'show'}
              onClick={(e) => {
                setMenuFor(a);
                placeMenu(e.currentTarget);
              }}
            >
              ⋯
            </button>
          </li>
        ))}
      </ul>
      <div id={menuId} ref={menuRef} className="activity-menu" popover="auto" role="menu" onToggle={handleMenuToggle}>
        <button type="button" role="menuitem" onClick={handleDelete}>
          Delete…
        </button>
      </div>
    </>
  );
}
