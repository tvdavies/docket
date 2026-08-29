import * as Dialog from '@radix-ui/react-dialog';
import { Command } from 'cmdk';

export type PaletteAction = { id: string; label: string; group: string; shortcut?: string; run(): void };

export function CommandPalette({ open, onOpenChange, actions }: { open: boolean; onOpenChange(open: boolean): void; actions: PaletteAction[] }) {
  const groups = new Map<string, PaletteAction[]>();
  for (const action of actions) { if (!groups.has(action.group)) groups.set(action.group, []); groups.get(action.group)!.push(action); }
  return <Dialog.Root open={open} onOpenChange={onOpenChange}><Dialog.Portal><Dialog.Overlay className="dialog-overlay palette-overlay" /><Dialog.Content className="palette-card" aria-describedby={undefined}>
    <Dialog.Title className="sr-only">Command palette</Dialog.Title><Command label="Command palette"><Command.Input autoFocus placeholder="Type a command or search…" /><Command.List><Command.Empty>No matching command.</Command.Empty>
      {[...groups].map(([group, items]) => <Command.Group key={group} heading={group}>{items.map((action) => <Command.Item key={action.id} value={`${action.label} ${group}`} onSelect={() => { action.run(); onOpenChange(false); }}><span>{action.label}</span>{action.shortcut && <kbd>{action.shortcut}</kbd>}</Command.Item>)}</Command.Group>)}
    </Command.List></Command>
  </Dialog.Content></Dialog.Portal></Dialog.Root>;
}
