const ELEMENT_NODE = 1;
const TEXT_NODE = 3;
const BLOCKS = new Set(['P', 'DIV', 'H1', 'H2', 'H3', 'H4', 'H5', 'H6', 'PRE', 'UL', 'OL', 'BLOCKQUOTE', 'TABLE', 'HR']);

function children(node) { return Array.from(node?.childNodes || []); }
function tag(node) { return String(node?.tagName || '').toUpperCase(); }
function attribute(node, name) { return node?.getAttribute?.(name) || ''; }
function textContent(node) {
  if (!node) return '';
  if (node.nodeType === TEXT_NODE) return String(node.nodeValue ?? node.textContent ?? '');
  if (typeof node.textContent === 'string') return node.textContent;
  return children(node).map(textContent).join('');
}
function escapeText(value) {
  return String(value).replace(/\u00a0/g, ' ').replace(/\\/g, '\\\\').replace(/([`*_[\]])/g, '\\$1');
}
function escapeDestination(value) { return String(value).replace(/\\/g, '\\\\').replace(/([()])/g, '\\$1'); }
function safeDestination(value) {
  const destination = String(value || '').trim();
  return /^(?:javascript|vbscript|data):/i.test(destination) ? '' : destination;
}
function hasBlockChildren(node) { return children(node).some((child) => child.nodeType === ELEMENT_NODE && BLOCKS.has(tag(child))); }
function serializeChildren(node, context = {}) {
  const blockChildren = hasBlockChildren(node);
  return children(node).map((child) => {
    if (blockChildren && child.nodeType === TEXT_NODE && !textContent(child).trim()) return '';
    return serialize(child, context);
  }).join('');
}
function fenceFor(value) {
  const runs = String(value).match(/`+/g) || [];
  return '`'.repeat(Math.max(3, ...runs.map((run) => run.length + 1)));
}
function inlineCode(value) {
  const content = String(value).replace(/\n+/g, ' ');
  const fence = fenceFor(content);
  const pad = /^\s|\s$/.test(content) ? ' ' : '';
  return `${fence}${pad}${content}${pad}${fence}`;
}
function listItem(node, marker, context) {
  const body = serializeChildren(node, { ...context, inList: true }).trim().replace(/\n{3,}/g, '\n\n');
  const lines = body.split('\n');
  const indent = ' '.repeat(marker.length);
  return `${marker}${lines.shift() || ''}${lines.length ? `\n${lines.map((line) => `${indent}${line}`).join('\n')}` : ''}`;
}
function serializeList(node, ordered, context) {
  let number = Number.parseInt(attribute(node, 'start') || '1', 10);
  if (!Number.isFinite(number)) number = 1;
  const items = children(node).filter((child) => tag(child) === 'LI').map((item, index) => {
    const marker = ordered ? `${number + index}. ` : '- ';
    return listItem(item, marker, context);
  });
  return `${items.join('\n')}\n\n`;
}
function descendants(node, target) {
  const found = [];
  for (const child of children(node)) {
    if (tag(child) === target) found.push(child);
    else found.push(...descendants(child, target));
  }
  return found;
}
function serializeTable(node) {
  const rows = descendants(node, 'TR').map((row) => children(row).filter((cell) => ['TH', 'TD'].includes(tag(cell))).map((cell) => serializeChildren(cell).trim().replace(/\|/g, '\\|').replace(/\n+/g, ' ')));
  if (!rows.length) return '';
  const width = Math.max(...rows.map((row) => row.length));
  const normalized = rows.map((row) => [...row, ...Array(Math.max(0, width - row.length)).fill('')]);
  const line = (row) => `| ${row.join(' | ')} |`;
  return `${line(normalized[0])}\n${line(Array(width).fill('---'))}\n${normalized.slice(1).map(line).join('\n')}${normalized.length > 1 ? '\n' : ''}\n`;
}
function serialize(node, context = {}) {
  if (!node) return '';
  if (node.nodeType === TEXT_NODE) return context.raw ? textContent(node) : escapeText(textContent(node));
  if (node.nodeType !== ELEMENT_NODE) return serializeChildren(node, context);
  const name = tag(node);
  if (name === 'BR') return '\n';
  if (name === 'P' || name === 'DIV') return `${serializeChildren(node, context).trim()}\n\n`;
  if (/^H[1-6]$/.test(name)) return `${'#'.repeat(Number(name[1]))} ${serializeChildren(node, context).trim()}\n\n`;
  if (name === 'STRONG' || name === 'B') return `**${serializeChildren(node, context)}**`;
  if (name === 'EM' || name === 'I') return `_${serializeChildren(node, context)}_`;
  if (name === 'DEL' || name === 'S' || name === 'STRIKE') return `~~${serializeChildren(node, context)}~~`;
  if (name === 'CODE' && tag(node.parentNode) !== 'PRE') return inlineCode(textContent(node));
  if (name === 'PRE') {
    const code = children(node).find((child) => tag(child) === 'CODE');
    const value = textContent(code || node).replace(/\n$/, '');
    const language = (attribute(code, 'class').match(/(?:^|\s)language-([^\s]+)/) || [])[1] || '';
    const fence = fenceFor(value);
    return `${fence}${language}\n${value}\n${fence}\n\n`;
  }
  if (name === 'A') {
    const label = serializeChildren(node, context).trim() || safeDestination(attribute(node, 'href'));
    const destination = safeDestination(attribute(node, 'href'));
    if (!destination) return label;
    const title = attribute(node, 'title').replace(/"/g, '\\"');
    return `[${label}](${escapeDestination(destination)}${title ? ` "${title}"` : ''})`;
  }
  if (name === 'BLOCKQUOTE') {
    const body = serializeChildren(node, context).trim();
    return `${body.split('\n').map((line) => `> ${line}`).join('\n')}\n\n`;
  }
  if (name === 'UL') return serializeList(node, false, context);
  if (name === 'OL') return serializeList(node, true, context);
  if (name === 'LI') return serializeChildren(node, context);
  if (name === 'INPUT' && String(attribute(node, 'type')).toLowerCase() === 'checkbox') return `${node.checked || attribute(node, 'checked') ? '[x]' : '[ ]'} `;
  if (name === 'TABLE') return serializeTable(node);
  if (name === 'HR') return '---\n\n';
  if (name === 'IMG') {
    const source = safeDestination(attribute(node, 'src'));
    return source ? `![${escapeText(attribute(node, 'alt'))}](${escapeDestination(source)})` : escapeText(attribute(node, 'alt'));
  }
  return serializeChildren(node, context);
}

export function markdownFromElement(root) {
  return serializeChildren(root)
    .replace(/[ \t]+\n/g, '\n')
    .replace(/\n{3,}/g, '\n\n')
    .trim();
}

export function normalizedTitle(value) {
  return String(value || '').replace(/[\r\n]+/g, ' ').replace(/\s+/g, ' ').trim();
}

export function plainPasteText(value, singleLine = false) {
  const text = String(value || '').replace(/\r\n?/g, '\n');
  return singleLine ? text.replace(/\n+/g, ' ') : text;
}
