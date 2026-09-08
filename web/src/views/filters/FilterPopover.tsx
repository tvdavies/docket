import { useId } from 'react';
import { Button } from '../../components/ui/button';
import { Checkbox } from '../../components/ui/checkbox';
import { Input } from '../../components/ui/input';
import { Popover, PopoverContent, PopoverTrigger } from '../../components/ui/popover';
import { activeFilterCount, allStatuses, emptyFilters, type Filters, type Preferences } from '../../store/preferences';
import type { BoardTask, StreamConfig } from '../../types';

type FilterGroup = Exclude<keyof Filters, 'query'>;
const humanize = (value: string) => value.replace(/[._-]+/g, ' ').replace(/\b\w/g, (letter) => letter.toUpperCase());
const groups: { key: FilterGroup; title: string; empty: string }[] = [
  { key: 'statuses', title: 'Status', empty: 'No statuses available' },
  { key: 'assignees', title: 'Assignee', empty: 'No assignees available' },
  { key: 'labels', title: 'Labels', empty: 'No labels available' },
  { key: 'projects', title: 'Project', empty: 'No projects available' },
  { key: 'states', title: 'State', empty: 'No states available' },
];
const optionLabel = (key: FilterGroup, value: string) => {
  if (key === 'assignees' && !value) return 'Unassigned';
  if (key === 'projects' && !value) return 'No project';
  return key === 'statuses' || key === 'states' ? humanize(value) : value;
};

export function FilterPopover({ tasks, preferences, config, update }: {
  tasks: BoardTask[]; preferences: Preferences; config: StreamConfig;
  update(change: (value: Preferences) => Preferences): void;
}) {
  const id = useId();
  const count = activeFilterCount(preferences.filters);
  const options: Record<FilterGroup, string[]> = {
    statuses: allStatuses(config, tasks),
    assignees: [...new Set(tasks.map((task) => task.assignee || ''))].sort(),
    labels: [...new Set([...config.labels, ...tasks.flatMap((task) => task.labels)])].sort(),
    projects: [...new Set(tasks.map((task) => task.project || ''))].sort(),
    states: ['open', 'terminal', 'waiting'],
  };
  return <Popover>
    <PopoverTrigger asChild><Button variant="outline" className="filter-trigger">Filter{count > 0 && <span className="filter-count">{count}</span>}</Button></PopoverTrigger>
    <PopoverContent className="filter-popover" align="end" sideOffset={8} collisionPadding={12} aria-labelledby={`${id}-heading`} aria-describedby={`${id}-help`}>
      <header className="filter-popover-header"><h2 id={`${id}-heading`}>Filters</h2><p id={`${id}-help`}>Changes apply immediately. Select multiple options in each group.</p></header>
      <div className="filter-popover-body">
        <label className="filter-search" htmlFor={`${id}-query`}>Search tasks<Input id={`${id}-query`} type="search" placeholder="Title, ID, assignee…" value={preferences.filters.query} onChange={(event) => update((value) => ({ ...value, filters: { ...value.filters, query: event.target.value } }))} /></label>
        <div className="filter-groups">{groups.map(({ key, title, empty }) => {
          // Keep selected values removable even if live updates remove their last task.
          const values = [...new Set([...options[key], ...preferences.filters[key]])];
          return <fieldset className="filter-group" key={key}><legend>{title}{preferences.filters[key].length > 0 && <span className="filter-count">{preferences.filters[key].length}</span>}</legend>
            {values.length === 0 ? <p className="muted">{empty}</p> : <div className="filter-options">{values.map((item, index) => {
              const checked = preferences.filters[key].includes(item);
              const optionId = `${id}-${key}-${index}`;
              return <label className="filter-option" data-checked={checked} htmlFor={optionId} key={item}>
                <Checkbox id={optionId} checked={checked} onCheckedChange={(next) => update((value) => ({ ...value, filters: { ...value.filters, [key]: next === true ? [...new Set([...value.filters[key], item])] : value.filters[key].filter((entry) => entry !== item) } }))} />
                <span>{optionLabel(key, item)}</span>
              </label>;
            })}</div>}
          </fieldset>;
        })}</div>
      </div>
      <footer className="filter-popover-footer"><span>{count ? `${count} active ${count === 1 ? 'filter' : 'filters'}` : 'No active filters'}</span><Button variant="outline" size="sm" disabled={!count} onClick={() => update((value) => ({ ...value, filters: emptyFilters() }))}>Reset filters</Button></footer>
    </PopoverContent>
  </Popover>;
}
