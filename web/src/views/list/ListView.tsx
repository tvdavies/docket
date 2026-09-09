import { useMemo, useRef, useState } from 'react';
import { useVirtualizer } from '@tanstack/react-virtual';
import { Button } from '../../components/ui/button';
import { Badge } from '../../components/ui/badge';
import type { BoardTask } from '../../types';

const humanize = (value: string) => value.replace(/[._-]+/g, ' ').replace(/\b\w/g, (letter) => letter.toUpperCase());
type Row = { kind: 'status'; status: string; count: number } | { kind: 'task'; task: BoardTask };

export function groupTaskRows(tasks: BoardTask[], statuses: string[], hidden: string[], collapsed: Set<string>, showEmpty: boolean): Row[] {
  const groups = new Map([...statuses, ...tasks.map((task) => task.status)].map((status) => [status, [] as BoardTask[]]));
  for (const task of tasks) groups.get(task.status)!.push(task);
  return [...groups].flatMap(([status, items]): Row[] => hidden.includes(status) || (!showEmpty && !items.length) ? [] : [
    { kind: 'status', status, count: items.length },
    ...(collapsed.has(status) ? [] : items.map((task): Row => ({ kind: 'task', task }))),
  ]);
}

export function ListView({ tasks, statuses = [], hiddenStatuses = [], showEmpty = false, selected, onSelect }: {
  tasks: BoardTask[]; statuses?: string[]; hiddenStatuses?: string[]; showEmpty?: boolean; selected: string; onSelect(task: string): void;
}) {
  const parent = useRef<HTMLDivElement>(null);
  const [collapsed, setCollapsed] = useState(new Set<string>());
  const rows = useMemo(() => groupTaskRows(tasks, statuses, hiddenStatuses, collapsed, showEmpty), [tasks, statuses, hiddenStatuses, collapsed, showEmpty]);
  const virtualizer = useVirtualizer({ count: rows.length, getScrollElement: () => parent.current,
    getItemKey: (index) => rows[index].kind === 'status' ? `status:${rows[index].status}` : `task:${rows[index].task.id}`,
    estimateSize: (index) => rows[index].kind === 'status' ? 42 : 48, overscan: 12 });
  return (
    <div className="list-view" ref={parent} aria-label="Task list">
      <div className="virtual-stack grouped-list" style={{ height: virtualizer.getTotalSize() }}>
        {virtualizer.getVirtualItems().map((item) => {
          const row = rows[item.index];
          if (row.kind === 'status') return <div key={item.key} className="virtual-row list-group" style={{ top: item.start, height: item.size }}>
            <Button variant="ghost" className="list-group-toggle" aria-expanded={!collapsed.has(row.status)} onClick={() => setCollapsed((current) => {
              const next = new Set(current); if (next.has(row.status)) next.delete(row.status); else next.add(row.status); return next;
            })}><span className="group-chevron" aria-hidden="true">{collapsed.has(row.status) ? '▸' : '▾'}</span><i className="status-dot" /><span>{humanize(row.status)}</span><span className="lane-count">{row.count}</span></Button>
          </div>;
          const task = row.task;
          return <button key={item.key} className={`list-row virtual-row ${selected === task.id ? 'selected' : ''}`} style={{ top: item.start, height: item.size }} onClick={() => onSelect(task.id)}>
            <span className="task-id">{task.id}</span><span className="list-title" title={task.title}>{task.title}</span>
            <span className="list-labels">{task.labels.slice(0, 3).map((label) => <Badge variant="secondary" key={label}>{label}</Badge>)}</span>
            <span className="list-assignee">{task.assignee || 'Unassigned'}</span><time dateTime={task.updated_at}>{new Date(task.updated_at).toLocaleDateString()}</time>
          </button>;
        })}
      </div>
    </div>
  );
}
