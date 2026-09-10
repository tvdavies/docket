import { useRef, useState } from 'react';
import { Badge } from '../../components/ui/badge';
import { Button } from '../../components/ui/button';
import { useCardDrag } from './useCardDrag';
import * as DropdownMenu from '@radix-ui/react-dropdown-menu';
import { defaultRangeExtractor, useVirtualizer } from '@tanstack/react-virtual';
import type { BoardTask, LivePayload, StreamConfig } from '../../types';
import type { Preferences } from '../../store/preferences';
import { allStatuses } from '../../store/preferences';
import { ResolvedReference } from '../../registry/ResolvedReference';
import { PluginCardHost } from '../../registry/PluginCardHost';

const humanize = (value: string) => value.replace(/[._-]+/g, ' ').replace(/\b\w/g, (letter) => letter.toUpperCase());
const relative = (value: string) => {
  const milliseconds = Date.parse(value) - Date.now();
  if (!Number.isFinite(milliseconds)) return '';
  const seconds = Math.round(milliseconds / 1000); const absolute = Math.abs(seconds);
  const [amount, unit] = absolute < 60 ? [seconds, 'second'] : absolute < 3600 ? [Math.round(seconds / 60), 'minute'] : absolute < 86400 ? [Math.round(seconds / 3600), 'hour'] : [Math.round(seconds / 86400), 'day'];
  return new Intl.RelativeTimeFormat(undefined, { numeric: 'auto' }).format(amount, unit as Intl.RelativeTimeFormatUnit);
};

export function BoardView(props: {
  workspace: string;
  tasks: BoardTask[];
  config: StreamConfig;
  preferences: Preferences;
  selected: string;
  live: LivePayload[];
  onSelect(task: string): void;
  onMove(task: BoardTask, status: string): void;
  onSettings?(status: string): void;
}) {
  const [dragging, setDragging] = useState('');
  const grouped = new Map(allStatuses(props.config, props.tasks).map((status) => [status, [] as BoardTask[]]));
  for (const task of props.tasks) {
    if (!grouped.has(task.status)) grouped.set(task.status, []);
    grouped.get(task.status)!.push(task);
  }
  const statuses = [...grouped.keys()].filter((status) => !props.preferences.hiddenStatuses.includes(status) && (props.preferences.showEmpty || grouped.get(status)!.length));
  return (
    <div className="board-scroll" aria-label="Task board">
      <div className="board-grid" style={{ gridTemplateColumns: `repeat(${Math.max(1, statuses.length)}, var(--lane-width))` }}>
        {statuses.map((status) => <Lane key={status} dragging={dragging} setDragging={setDragging} workspace={props.workspace} status={status} tasks={grouped.get(status)!} config={props.config} preferences={props.preferences} selected={props.selected} live={props.live} onSelect={props.onSelect} onMove={props.onMove} onSettings={props.onSettings} />)}
      </div>
    </div>
  );
}

function Lane({ workspace, status, tasks, dragging, setDragging, config, preferences, selected, live, onSelect, onMove, onSettings }: {
  workspace: string; status: string; tasks: BoardTask[]; dragging: string; setDragging(task: string): void; config: StreamConfig; preferences: Preferences; selected: string; live: LivePayload[];
  onSelect(task: string): void; onMove(task: BoardTask, status: string): void; onSettings?(status: string): void;
}) {
  const parent = useRef<HTMLDivElement>(null);
  const draggedIndex = tasks.findIndex((task) => task.id === dragging);
  const virtualizer = useVirtualizer({ count: tasks.length, getScrollElement: () => parent.current, getItemKey: (index) => tasks[index].id, estimateSize: () => 160, overscan: 6,
    rangeExtractor: (range) => [...new Set([...defaultRangeExtractor(range), ...(draggedIndex >= 0 ? [draggedIndex] : [])])].sort((a, b) => a - b),
  });
  return (
    <section className={`lane ${config.terminal.includes(status) ? 'terminal' : ''}`} data-status={status}>
      <header className="lane-header"><span className="status-dot" /><h2>{humanize(status)}</h2><span className="lane-count">{tasks.length}</span>{onSettings && config.statuses.includes(status) && <Button size="icon" variant="ghost" aria-label={`Plugin settings for ${humanize(status)}`} onClick={() => onSettings(status)}>⚙</Button>}</header>
      <div className="lane-list" ref={parent}>
        {!tasks.length && <div className="lane-empty">No tasks</div>}
        <div className="virtual-stack" style={{ height: virtualizer.getTotalSize() }}>
          {virtualizer.getVirtualItems().map((item) => {
            const task = tasks[item.index];
            return <div key={task.id} ref={virtualizer.measureElement} data-index={item.index} className={`virtual-row ${dragging === task.id ? 'drag-placeholder' : ''}`} style={{ top: item.start, ...(dragging === task.id ? { height: item.size } : {}) }}><TaskCard workspace={workspace} task={task} selected={selected === task.id} config={config} preferences={preferences} live={live} onSelect={onSelect} onMove={onMove} onDragging={(active) => setDragging(active ? task.id : '')} /></div>;
          })}
        </div>
      </div>
    </section>
  );
}

export function TaskCard({ workspace, task, selected, config, preferences, live, onSelect, onMove, onDragging = () => undefined }: {
  workspace: string; task: BoardTask; selected: boolean; config: StreamConfig; preferences: Preferences; live: LivePayload[];
  onSelect(task: string): void; onMove(task: BoardTask, status: string): void; onDragging?(active: boolean): void;
}) {
  const drag = useCardDrag(onDragging, (status) => { if (status !== task.status) onMove(task, status); });
  const liveForTask = live.filter((item) => item.task === task.id);
  return (
    <article className={`task-card ${selected ? 'selected' : ''}`} role="link" aria-current={selected ? 'true' : undefined} tabIndex={0} data-task={task.id}
      onPointerDown={drag.onPointerDown} onDragStart={(event) => event.preventDefault()}
      onKeyDown={(event) => { if (event.target === event.currentTarget && (event.key === ' ' || event.key === 'Enter')) { event.preventDefault(); event.stopPropagation(); onSelect(task.id); } }}
      onClick={(event) => { if (!drag.suppressClick.current && !(event.target as HTMLElement).closest('button, a, [role="menuitem"]')) onSelect(task.id); }}>
      <div className="card-top"><span className="card-drag-handle" aria-hidden="true" title="Drag task">⠿</span><span className="task-id">{task.id}</span>{task.wait && <span className="wait-pill">Waiting · {humanize(task.wait.kind)}</span>}
        <DropdownMenu.Root><DropdownMenu.Trigger asChild><Button variant="ghost" size="icon" className="card-menu" aria-label={`Move ${task.id}`} onClick={(event) => event.stopPropagation()}>•••</Button></DropdownMenu.Trigger>
          <DropdownMenu.Portal><DropdownMenu.Content className="menu-content" align="end">
            <DropdownMenu.Label className="menu-label">Move to</DropdownMenu.Label>
            {allStatuses(config, [task]).map((status) => <DropdownMenu.Item className="menu-item" disabled={status === task.status} key={status} onSelect={() => onMove(task, status)}>{humanize(status)}</DropdownMenu.Item>)}
          </DropdownMenu.Content></DropdownMenu.Portal>
        </DropdownMenu.Root>
      </div>
      <h3>{task.title}</h3>
      {liveForTask.length > 0 && <span className="live-pill">● {liveForTask[0].kind}</span>}
      <PluginCardHost workspace={workspace} task={task} />
      {preferences.fields.references && task.references[0] && <ResolvedReference reference={task.references[0]} compact />}
      <footer className="card-meta">
        {preferences.fields.labels && task.labels.slice(0, 3).map((label) => <Badge variant="secondary" className="label" key={label}>{label}</Badge>)}
        {preferences.fields.project && task.project && <span>{task.project}</span>}
        {preferences.fields.assignee && task.assignee && <span className="assignee">{task.assignee}</span>}
        {preferences.fields.updated && <time dateTime={task.updated_at}>{relative(task.updated_at)}</time>}
      </footer>
    </article>
  );
}
