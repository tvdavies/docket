import { describe, expect, test } from 'bun:test';
import { markdownFromElement, normalizedTitle, plainPasteText } from './markdown-edit.js';

const text = (value) => ({ nodeType: 3, nodeValue: value, textContent: value });
function node(tagName, attrs = {}, ...childNodes) {
  const item = {
    nodeType: 1, tagName, childNodes, parentNode: null,
    getAttribute(name) { return attrs[name] ?? ''; },
    get textContent() { return this.childNodes.map((child) => child.textContent || '').join(''); },
  };
  childNodes.forEach((child) => { child.parentNode = item; });
  if ('checked' in attrs) item.checked = attrs.checked;
  return item;
}
const root = (...children) => ({ nodeType: 1, tagName: 'DIV', childNodes: children });

describe('contenteditable Markdown serialization', () => {
  test('serializes rendered headings, inline formatting, links, quotes, and code', () => {
    const rendered = root(
      node('H2', {}, text('Plan')),
      node('P', {}, text('Use '), node('STRONG', {}, text('safe')), text(' '), node('A', { href: 'https://example.test/a_(b)' }, text('link')), text('.')),
      node('BLOCKQUOTE', {}, node('P', {}, text('A note'))),
      node('PRE', {}, node('CODE', { class: 'language-go' }, text('fmt.Println("ok")\n'))),
    );
    expect(markdownFromElement(rendered)).toBe('## Plan\n\nUse **safe** [link](https://example.test/a_\\(b\\)).\n\n> A note\n\n```go\nfmt.Println("ok")\n```');
  });

  test('serializes task lists, ordered lists, and tables', () => {
    const rendered = root(
      node('UL', {},
        node('LI', {}, node('INPUT', { type: 'checkbox', checked: true }), text(' Done')),
        node('LI', {}, node('INPUT', { type: 'checkbox', checked: false }), text(' Next')),
      ),
      node('OL', { start: '3' }, node('LI', {}, text('Third')), node('LI', {}, text('Fourth'))),
      node('TABLE', {},
        node('THEAD', {}, node('TR', {}, node('TH', {}, text('Name')), node('TH', {}, text('State')))),
        node('TBODY', {}, node('TR', {}, node('TD', {}, text('Docket')), node('TD', {}, text('Ready')))),
      ),
    );
    expect(markdownFromElement(rendered)).toBe('- [x]  Done\n- [ ]  Next\n\n3. Third\n4. Fourth\n\n| Name | State |\n| --- | --- |\n| Docket | Ready |');
  });

  test('normalizes title and plain-text paste without accepting HTML', () => {
    expect(normalizedTitle('  A\n new   title ')).toBe('A new title');
    expect(plainPasteText('<b>literal</b>\r\nnext')).toBe('<b>literal</b>\nnext');
    expect(plainPasteText('one\ntwo', true)).toBe('one two');
  });
});
