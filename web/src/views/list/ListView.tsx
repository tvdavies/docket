import { useRef } from 'react';
import { useVirtualizer } from '@tanstack/react-virtual';
import type { BoardTask } from '../../types';

const humanize = (value: string) => value.replace(/[._-]+/g, ' ').replace(/\b\w/g, (letter) => letter.toUpperCase());

export function ListView({ tasks, selected, onSelect }: { tasks: BoardTask[]; selected: string; onSelect(task: string): void }) {
  const parent = useRef<HTMLDivElement>(null);
  const virtualizer = useVirtualizer({ count: tasks.length, getScrollElement: () => parent.current, estimateSize: () => 54, overscan: 10 });
  return (
    <div className="list-view" ref={parent} role="grid" aria-label="Task list">
      <div className="list-header" role="row"><span>Status</span><span>Task</span><span>Assignee</span><span>Labels</span><span>Updated</span></div>
      <div className="virtual-stack" style={{ height: virtualizer.getTotalSize() }}>
        {virtualizer.getVirtualItems().map((item) => {
          const task = tasks[item.index];
          return <button key={task.id} className={`list-row virtual-row ${selected === task.id ? 'selected' : ''}`} style={{ transform: `translateY(${item.start}px)` }} onClick={() => onSelect(task.id)} role="row">
            <span><i className="status-dot" />{humanize(task.status)}</span>
            <span className="list-title"><b>{task.id}</b>{task.title}</span>
            <span>{task.assignee || '—'}</span><span>{task.labels.join(', ') || '—'}</span><time dateTime={task.updated_at}>{new Date(task.updated_at).toLocaleDateString()}</time>
          </button>;
        })}
      </div>
    </div>
  );
}
