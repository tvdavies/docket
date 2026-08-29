import { useRef } from 'react';
import * as DropdownMenu from '@radix-ui/react-dropdown-menu';
import { useVirtualizer } from '@tanstack/react-virtual';
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
}) {
  const grouped = new Map(allStatuses(props.config, props.tasks).map((status) => [status, [] as BoardTask[]]));
  for (const task of props.tasks) {
    if (!grouped.has(task.status)) grouped.set(task.status, []);
    grouped.get(task.status)!.push(task);
  }
  const statuses = [...grouped.keys()].filter((status) => !props.preferences.hiddenStatuses.includes(status) && (props.preferences.showEmpty || grouped.get(status)!.length));
  return (
    <div className="board-scroll" aria-label="Task board">
      <div className="board-grid" style={{ gridTemplateColumns: `repeat(${Math.max(1, statuses.length)}, minmax(280px, 1fr))` }}>
        {statuses.map((status) => <Lane key={status} workspace={props.workspace} status={status} tasks={grouped.get(status)!} config={props.config} preferences={props.preferences} selected={props.selected} live={props.live} onSelect={props.onSelect} onMove={props.onMove} />)}
      </div>
    </div>
  );
}

function Lane({ workspace, status, tasks, config, preferences, selected, live, onSelect, onMove }: {
  workspace: string; status: string; tasks: BoardTask[]; config: StreamConfig; preferences: Preferences; selected: string; live: LivePayload[];
  onSelect(task: string): void; onMove(task: BoardTask, status: string): void;
}) {
  const parent = useRef<HTMLDivElement>(null);
  const virtualizer = useVirtualizer({ count: tasks.length, getScrollElement: () => parent.current, estimateSize: () => 148, overscan: 6 });
  return (
    <section className={`lane ${config.terminal.includes(status) ? 'terminal' : ''}`} data-status={status}
      onDragOver={(event) => { event.preventDefault(); event.currentTarget.dataset.drag = 'true'; }}
      onDragLeave={(event) => { if (!event.currentTarget.contains(event.relatedTarget as Node)) delete event.currentTarget.dataset.drag; }}
      onDrop={(event) => { event.preventDefault(); delete event.currentTarget.dataset.drag; const id = event.dataTransfer.getData('text/plain'); const task = tasks.find((item) => item.id === id); if (task) onMove(task, status); }}>
      <header className="lane-header"><span className="status-dot" /><h2>{humanize(status)}</h2><span className="lane-count">{tasks.length}</span></header>
      <div className="lane-list" ref={parent}>
        {!tasks.length && <div className="lane-empty">No tasks</div>}
        <div className="virtual-stack" style={{ height: virtualizer.getTotalSize() }}>
          {virtualizer.getVirtualItems().map((item) => {
            const task = tasks[item.index];
            return <div key={task.id} className="virtual-row" style={{ transform: `translateY(${item.start}px)` }}><TaskCard workspace={workspace} task={task} selected={selected === task.id} config={config} preferences={preferences} live={live} onSelect={onSelect} onMove={onMove} /></div>;
          })}
        </div>
      </div>
    </section>
  );
}

export function TaskCard({ workspace, task, selected, config, preferences, live, onSelect, onMove }: {
  workspace: string; task: BoardTask; selected: boolean; config: StreamConfig; preferences: Preferences; live: LivePayload[];
  onSelect(task: string): void; onMove(task: BoardTask, status: string): void;
}) {
  const liveForTask = live.filter((item) => item.task === task.id);
  return (
    <article className={`task-card ${selected ? 'selected' : ''}`} tabIndex={selected ? 0 : -1} data-task={task.id} draggable
      onDragStart={(event) => { event.dataTransfer.setData('text/plain', task.id); event.dataTransfer.effectAllowed = 'move'; }}
      onClick={() => onSelect(task.id)} onDoubleClick={() => onSelect(task.id)}>
      <div className="card-top"><span className="task-id">{task.id}</span>{task.wait && <span className="wait-pill">Waiting · {humanize(task.wait.kind)}</span>}
        <DropdownMenu.Root><DropdownMenu.Trigger asChild><button className="icon-button card-menu" aria-label={`Move ${task.id}`} onClick={(event) => event.stopPropagation()}>•••</button></DropdownMenu.Trigger>
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
        {preferences.fields.labels && task.labels.slice(0, 3).map((label) => <span className="label" key={label}>{label}</span>)}
        {preferences.fields.project && task.project && <span>{task.project}</span>}
        {preferences.fields.assignee && task.assignee && <span className="assignee">{task.assignee}</span>}
        {preferences.fields.updated && <time dateTime={task.updated_at}>{relative(task.updated_at)}</time>}
      </footer>
    </article>
  );
}
