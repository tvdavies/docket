import { chromium } from 'playwright';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { settingsSandbox, secretSentinel } from './settings-sandbox';

if (process.argv.slice(2).some((arg) => arg !== '--preview')) throw new Error('Only --preview is supported. External board URLs are never accepted.');
const sandbox = await settingsSandbox();
if (process.argv.includes('--preview')) {
  console.log('Fixture preview only; no test mutations. Ctrl+C stops the owned service and removes its temporary files.');
  await new Promise<void>((done) => { process.once('SIGINT', () => done()); process.once('SIGTERM', () => done()); });
  await sandbox.cleanup();
  process.exit(0);
}
const evidence = resolve(process.env.SETTINGS_EVIDENCE_DIR || join('/tmp', 'docket-settings-evidence'));
await mkdir(evidence, { recursive: true }).catch(async (cause) => { await sandbox.cleanup(); throw cause; });
const browser = await chromium.launch({ executablePath: process.env.CHROMIUM_PATH, headless: true, args: ['--no-sandbox'] }).catch(async (cause) => { await sandbox.cleanup(); throw cause; });
const interrupted = async () => { await browser.close(); await sandbox.cleanup(); process.exit(130); };
process.once('SIGINT', interrupted); process.once('SIGTERM', interrupted);
const page = await browser.newPage({ viewport: { width: 1440, height: 1040 } });
page.setDefaultTimeout(12000);
const errors: string[] = [];
const bodies: string[] = [];
const passes: string[] = [];
page.on('pageerror', (cause) => errors.push(cause.message));
page.on('request', (request) => { if (request.postData()) bodies.push(request.postData()!); });
const assert = (value: unknown, text: string) => { if (!value) throw new Error(text); passes.push(text); console.log('PASS', text); };
const catalogue = async () => (await (await sandbox.request('/api/plugins')).json())[0];
const bytes = async () => Promise.all([sandbox.registry, sandbox.configPath('alpha'), sandbox.configPath('beta')].map((path) => readFile(path, 'utf8')));
const screenshot = async (name: string) => {
  if ((await page.content()).includes(secretSentinel) || bodies.join('').includes(secretSentinel) || (await page.evaluate(() => JSON.stringify({ ...localStorage, ...sessionStorage }))).includes(secretSentinel)) throw new Error('Secret sentinel leaked before screenshot');
  await page.screenshot({ path: join(evidence, name + '.png') });
};
async function open(path: string) { await page.goto(sandbox.url + path); await page.locator('.settings-field').first().waitFor(); }
async function edit(key: string, value: string | boolean, choice = false) {
  const field = page.locator(`[data-key="${key}"]`);
  const set = field.getByRole('button', { name: /^Set value for / });
  if (await set.count()) await set.click();
  if (typeof value === 'boolean') await field.getByRole('checkbox').setChecked(value);
  else if (choice) await field.locator('select').selectOption({ value });
  else await field.locator('input, textarea').fill(value);
}
async function save() {
  await page.getByRole('button', { name: 'Save', exact: true }).click();
  await page.locator('[data-status="saved"]').waitFor();
  await page.waitForFunction(() => !document.querySelector('.settings-dirty')?.textContent);
}
async function waitWorkspace(name: string, state: string, errorText = '') {
  for (let attempt = 0; attempt < 350; attempt++) {
    const values = await (await sandbox.request('/api/workspaces')).json();
    if (values.some((item: { name: string; state: string; last_error?: string }) => item.name === name && item.state === state && (!errorText || item.last_error?.includes(errorText)))) return;
    await Bun.sleep(100);
  }
  throw new Error(`Workspace ${name} did not become ${state}`);
}
async function navigateBoard(name: string) {
  await page.getByRole('combobox', { name: 'Workspace', exact: true }).selectOption(name);
  await page.waitForURL(`**/workspaces/${name}/settings/plugins`);
  await page.locator('[data-key="board_label"]').waitFor();
}
try {
  await open('/settings/plugins');
  assert(await page.locator('h1').evaluate((node) => node === document.activeElement), 'Settings heading receives focus on entry');
  assert((await page.locator('[data-key="api_token"] input, [data-key="api_token"] textarea').count()) === 0, 'Secrets have environment instructions but no input');
  assert((await page.locator('[data-key="max_parallel"]').innerText()).includes('stored or default'), 'Instance provenance does not pretend to identify stored defaults');
  await screenshot('instance-light');
  // Keyboard-only editing from the heading, using native focus navigation.
  for (let count = 0; count < 40; count++) {
    if (await page.locator('[data-key="api_base"] input').evaluate((node) => node === document.activeElement)) break;
    await page.keyboard.press('Tab');
  }
  assert(await page.locator('[data-key="api_base"] input').evaluate((node) => node === document.activeElement), 'Keyboard Tab reaches the labelled generated text field');
  await page.keyboard.press('ControlOrMeta+A'); await page.keyboard.type('https://keyboard.invalid');
  assert(await page.locator('[data-key="api_base"] input').inputValue() === 'https://keyboard.invalid', 'Keyboard edits are retained');
  await page.getByRole('button', { name: 'Set value for Greeting', exact: true }).focus();
  await page.keyboard.press('Enter');
  assert(await page.locator('[data-key="greeting"] input').evaluate((node) => node === document.activeElement), 'Keyboard Set value focuses the new editor instead of losing focus');
  await page.keyboard.type('keyboard greeting');
  assert(await page.locator('[data-key="greeting"] input').inputValue() === 'keyboard greeting', 'An unset field can be activated and edited without another pointer action');
  await edit('api_base', ''); await edit('max_parallel', '0'); await edit('telemetry', false);
  await edit('log_level', '0', true); await edit('retry_backoff', '2', true);
  await edit('allowed_hosts', '[1,false,{"nested":[null,"x"]}]'); await edit('headers', '{"nested":{"enabled":true}}'); await edit('greeting', '  hello  ');
  await save();
  let data = await catalogue();
  assert(data.instance_values.api_base === '' && data.instance_values.max_parallel === 0 && data.instance_values.telemetry === false && data.instance_values.retry_backoff === 5 && data.instance_values.allowed_hosts[2].nested[0] === null && data.instance_values.headers.nested.enabled && data.instance_values.greeting === '  hello  ', 'Every instance kind persists literal empty/zero/false and nested JSON through the real API');
  const initial = await bytes();
  assert(initial[0].includes('greeting:') && !initial[1].includes('greeting:'), 'Instance writes own only the temporary registry');
  await page.reload(); await page.locator('[data-key="greeting"] input').waitFor();
  assert(await page.locator('[data-key="greeting"] input').inputValue() === '  hello  ', 'Saved configuration survives a browser reload');
  await sandbox.request('/api/plugins/settings-fixture/config', { method: 'PATCH', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ values: { greeting: 'changed outside browser' } }) });
  await page.evaluate(() => window.dispatchEvent(new Event('focus')));
  await page.waitForFunction(() => document.querySelector<HTMLInputElement>('[data-key="greeting"] input')?.value === 'changed outside browser');
  assert(true, 'Window focus reloads external configuration changes without task SSE');
  await page.getByRole('link', { name: 'Board settings · alpha', exact: true }).click();
  await page.locator('[data-key="board_label"] input').waitFor();
  assert((await page.locator('[data-key="max_parallel"]').innerText()).includes('default 4'), 'Board defaults override same-key instance values');
  assert((await page.locator('[data-key="telemetry"]').innerText()).includes('instance value false'), 'Absent board keys explain instance fallback separately');
  assert(await page.getByRole('combobox', { name: 'Lane settings', exact: true }).locator('option').count() === 7, 'Lane selector includes all composed lanes, even empty ones');
  await screenshot('board-light');
  await edit('board_label', 'alpha edited'); await edit('max_parallel', '3.5'); await edit('auto_assign', true); await edit('telemetry', true);
  await edit('review_mode', '1', true); await edit('priority', '2', true); await edit('reviewers', '["a",false,2]'); await edit('routing', '{"nested":{"board":"alpha"}}');
  await save(); data = await catalogue();
  assert(data.workspace_values.alpha.config.board_label === 'alpha edited' && data.workspace_values.alpha.config.priority === 3 && data.workspace_values.alpha.config.review_mode === 'strict' && data.workspace_values.alpha.config.auto_assign === true && data.workspace_values.alpha.config.reviewers[1] === false && data.workspace_values.alpha.config.routing.nested.board === 'alpha', 'Every board kind persists through its owning PATCH endpoint');
  assert((await bytes())[2] === initial[2], 'Alpha board writes leave beta config bytes unchanged');
  await page.getByRole('combobox', { name: 'Lane settings', exact: true }).selectOption('in-review');
  await page.locator('[data-key="agent"]').waitFor();
  await screenshot('lane-light');
  await edit('agent', ''); await edit('wip_limit', '0'); await edit('autostart', false); await edit('mode', '1', true); await edit('channels', '1', true); await edit('watchers', '["a",{"b":false}]'); await edit('env', '{"nested":{"count":2}}');
  await save(); data = await catalogue();
  const lane = data.workspace_values.alpha.statuses['in-review'];
  assert(lane.agent === '' && lane.wip_limit === 0 && lane.autostart === false && lane.mode === 'auto' && lane.channels[1] === 'slack' && lane.watchers[1].b === false && lane.env.nested.count === 2, 'Every lane kind including compound enums persists with typed values');
  assert(!data.workspace_values.alpha.statuses.backlog && Object.keys(data.workspace_values.beta.statuses).length === 0, 'Lane writes do not cascade to other lanes or workspaces');
  const validBytes = await bytes();
  await edit('watchers', '[invalid');
  assert(await page.locator('[data-key="watchers"] textarea').inputValue() === '[invalid' && await page.getByRole('button', { name: 'Save', exact: true }).isDisabled(), 'Invalid JSON remains a raw draft and cannot be submitted');
  await page.locator('[data-key="watchers"]').scrollIntoViewIfNeeded(); await screenshot('validation-light');
  assert(JSON.stringify(await bytes()) === JSON.stringify(validBytes), 'Local validation leaves every stored file byte-identical');
  await page.getByRole('button', { name: 'Discard changes', exact: true }).click();
  // Dirty navigation cancellation preserves browser history in both directions.
  await edit('agent', 'unsaved-lane');
  await page.waitForFunction(() => document.querySelector('.settings-dirty')?.textContent?.includes('1 unsaved'));
  const laneURL = page.url();
  page.once('dialog', (dialog) => dialog.dismiss()); await page.goBack();
  await page.waitForURL(laneURL);
  assert(await page.locator('[data-key="agent"] input').inputValue() === 'unsaved-lane', 'Cancelled Back restores URL and keeps drafts');
  page.once('dialog', (dialog) => dialog.dismiss()); await page.getByRole('combobox', { name: 'Workspace', exact: true }).selectOption('beta');
  assert(page.url() === laneURL, 'Cancelled workspace switch keeps the owning scope');
  page.once('dialog', (dialog) => dialog.dismiss()); await page.getByRole('link', { name: 'Classic', exact: true }).click();
  assert(page.url() === laneURL, 'Classic navigation uses the same unsaved confirmation');
  await page.keyboard.press('ControlOrMeta+k');
  page.once('dialog', (dialog) => dialog.dismiss()); await page.getByRole('option', { name: 'Board settings · beta' }).click();
  assert(page.url() === laneURL && await page.locator('[data-key="agent"] input').inputValue() === 'unsaved-lane', 'Command palette navigation cannot discard settings silently');
  await page.getByRole('button', { name: 'Discard changes', exact: true }).click();
  await page.goBack(); await page.locator('[data-key="board_label"] input').waitFor();
  await edit('board_label', 'unsaved-board');
  await page.waitForFunction(() => document.querySelector('.settings-dirty')?.textContent?.includes('1 unsaved'));
  const boardURL = page.url(); page.once('dialog', (dialog) => dialog.dismiss()); await page.goForward(); await page.waitForURL(boardURL);
  assert(await page.locator('[data-key="board_label"] input').inputValue() === 'unsaved-board', 'Cancelled Forward restores both history and board draft');
  await page.getByRole('button', { name: 'Discard changes', exact: true }).click();
  await navigateBoard('beta');
  assert(await page.locator('[data-key="board_label"] input').inputValue() === 'beta', 'Workspace B never receives A drafts');
  // A delayed save is tied to beta. Leaving while it is pending cannot pretend to undo it.
  await edit('board_label', 'delayed-beta');
  let releaseWrite!: () => void;
  let capturedWrite!: () => void;
  const reachedWrite = new Promise<void>((done) => { capturedWrite = done; });
  await page.route('**/api/workspaces/beta/plugins/settings-fixture/config', async (route) => {
    capturedWrite(); await new Promise<void>((done) => { releaseWrite = done; }); await route.continue();
  });
  await page.getByRole('button', { name: 'Save', exact: true }).click(); await reachedWrite;
  assert(await page.locator('[data-key="board_label"] input').isDisabled(), 'Pending save disables editing and duplicate submit');
  page.once('dialog', (dialog) => dialog.accept()); await page.getByRole('combobox', { name: 'Workspace', exact: true }).selectOption('alpha');
  assert(page.url().endsWith('/workspaces/beta/settings/plugins'), 'Pending cross-workspace navigation waits for the captured save result');
  releaseWrite(); await page.locator('[data-status="saved"]').waitFor(); await page.waitForFunction(() => !document.querySelector('.settings-dirty')?.textContent);
  await page.unroute('**/api/workspaces/beta/plugins/settings-fixture/config');
  await navigateBoard('alpha');
  assert(await page.locator('[data-key="board_label"] input').inputValue() === 'alpha edited', 'Delayed beta response cannot overwrite alpha state');
  await navigateBoard('beta');
  const unloadGuard = await page.evaluate(() => { const event = new Event('beforeunload', { cancelable: true }); window.dispatchEvent(event); return event.defaultPrevented; });
  assert(!unloadGuard, 'Clean forms do not block page unload');
  // Real validation failures against all scopes while an independent UI draft is retained.
  await edit('board_label', 'retained-draft');
  await page.waitForFunction(() => document.querySelector('.settings-dirty')?.textContent?.includes('1 unsaved'));
  assert(await page.evaluate(() => { const event = new Event('beforeunload', { cancelable: true }); window.dispatchEvent(event); return event.defaultPrevented; }), 'Unsaved settings register the browser unload guard');
  const rejectedBefore = await bytes();
  for (const [path, values] of [
    ['/api/plugins/settings-fixture/config', { max_parallel: 'bad' }],
    ['/api/plugins/settings-fixture/config', { log_level: 'bad' }],
    ['/api/workspaces/beta/plugins/settings-fixture/config', { priority: 99 }],
    ['/api/workspaces/beta/plugins/settings-fixture/config', { mystery: 'bad' }],
    ['/api/workspaces/beta/plugins/settings-fixture/statuses/backlog', { channels: ['bad'] }],
  ] as const) {
    const response = await sandbox.request(path, { method: 'PATCH', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ values }) });
    assert(response.status === 400, `Real endpoint rejects invalid candidate: ${Object.keys(values)[0]}`);
  }
  assert(JSON.stringify(await bytes()) === JSON.stringify(rejectedBefore) && await page.locator('[data-key="board_label"] input').inputValue() === 'retained-draft', 'Type/enum/unknown-key errors preserve stored bytes and the current draft');
  // Transport failure: no write; uncertain outcome requires read-only reload.
  await page.route('**/api/workspaces/beta/plugins/settings-fixture/config', (route) => route.abort('failed'));
  await page.getByRole('button', { name: 'Save', exact: true }).click(); await page.locator('[data-status="uncertain"]').waitFor();
  assert(await page.locator('[data-key="board_label"] input').inputValue() === 'retained-draft', 'Lost save response retains the draft and reports uncertainty');
  await page.unroute('**/api/workspaces/beta/plugins/settings-fixture/config');
  await page.getByRole('button', { name: 'Reload current values', exact: true }).click(); await page.locator('[data-status="idle"]').waitFor();
  assert(JSON.stringify(await bytes()) === JSON.stringify(rejectedBefore), 'Uncertain recovery reload does not replay a PATCH');
  await save();
  // Success/readback failure then retry: normal PATCH reaches Go, only GET is faulted.
  await edit('board_label', 'saved-with-read-failure');
  let reads = 0;
  await page.route('**/api/plugins', (route) => ++reads >= 2 ? route.fulfill({ status: 503, contentType: 'application/json', body: '{"error":"fixture read unavailable"}' }) : route.continue());
  await page.getByRole('button', { name: 'Save', exact: true }).click(); await page.locator('[data-status="saved-reload-failed"]').waitFor();
  assert((await catalogue()).workspace_values.beta.config.board_label === 'saved-with-read-failure', 'A confirmed save remains persisted when read-back fails');
  const writesBeforeReload = bodies.length;
  await page.unroute('**/api/plugins'); await page.getByRole('button', { name: 'Reload current values', exact: true }).click(); await page.locator('[data-status="idle"]').waitFor();
  assert(bodies.length === writesBeforeReload, 'Read-back retry never resubmits a confirmed save');
  if (await page.getByRole('button', { name: 'Review refreshed values', exact: true }).count()) await page.getByRole('button', { name: 'Review refreshed values', exact: true }).click();
  await page.getByRole('link', { name: 'Instance settings', exact: true }).click(); await page.locator('[data-key="greeting"] input').waitFor();
  // Required absence in beta makes the real instance save fail atomically.
  const betaGood = await readFile(sandbox.configPath('beta'), 'utf8');
  await writeFile(sandbox.configPath('beta'), betaGood.replace(/^[ \t]*board_label:.*\n/m, ''));
  await edit('greeting', 'required-rejection-draft');
  const requiredBefore = await bytes();
  await page.getByRole('button', { name: 'Save', exact: true }).click(); await page.locator('[data-status="failed"]').waitFor();
  assert((await page.locator('[data-status="failed"]').innerText()).includes('required') && await page.locator('[data-key="greeting"] input').inputValue() === 'required-rejection-draft' && JSON.stringify(await bytes()) === JSON.stringify(requiredBefore), 'Real required-field rejection keeps instance draft and all stored bytes');
  await writeFile(sandbox.configPath('beta'), betaGood);
  await waitWorkspace('beta', 'watching');
  await page.getByRole('button', { name: 'Discard changes', exact: true }).click();
  // Version change stops writes without interpreting a new version as unsupported.
  await edit('greeting', 'version-draft');
  const manifest = join(sandbox.plugin, 'docket-plugin.yaml'); const originalManifest = await readFile(manifest, 'utf8');
  await writeFile(manifest, originalManifest.replace('version: 1.2.0', 'version: 99.0.0'));
  await page.getByRole('button', { name: 'Save', exact: true }).click(); await page.getByRole('button', { name: 'Review refreshed values', exact: true }).waitFor();
  assert(await page.locator('[data-key="greeting"] input').inputValue() === 'version-draft', 'Changed manifest version blocks stale saves with draft intact');
  await page.getByRole('button', { name: 'Review refreshed values', exact: true }).click(); await save();
  assert((await catalogue()).version === '99.0.0', 'Unknown plugin versions work when their declared schema is supported');
  // Failed catalogue, including malformed response, is never an empty plugin list.
  await edit('greeting', 'offline-draft');
  await page.route('**/api/plugins', (route) => route.fulfill({ status: 200, contentType: 'application/json', body: '{invalid' }));
  await page.getByRole('button', { name: 'Reload configuration', exact: true }).click();
  await page.getByRole('alert').filter({ hasText: 'Cached forms cannot be saved' }).waitFor();
  assert(await page.getByRole('button', { name: 'Save', exact: true }).isDisabled() && await page.locator('[data-key="greeting"] input').inputValue() === 'offline-draft', 'Malformed catalogue fails closed with stale cached drafts');
  await page.unroute('**/api/plugins'); await page.getByRole('button', { name: 'Reload configuration', exact: true }).click();
  await page.waitForFunction(() => !document.querySelector('.settings-page > [role="alert"]'));
  await page.getByRole('button', { name: 'Discard changes', exact: true }).click();
  // Mobile/dark evidence and local validation.
  await page.emulateMedia({ colorScheme: 'dark' }); await page.setViewportSize({ width: 390, height: 844 });
  await page.locator('.settings-page').evaluate((node) => { node.scrollTop = 0; });
  await screenshot('instance-mobile-dark');
  assert(await page.evaluate(() => document.documentElement.scrollWidth === innerWidth) && await page.locator('.settings-page').evaluate((node) => node.scrollWidth <= node.clientWidth) && await page.evaluate(() => document.documentElement.scrollHeight <= innerHeight), 'Mobile settings and JSON editors fit without horizontal overflow');
  await edit('allowed_hosts', '{wrong kind}'); await page.locator('[data-key="allowed_hosts"]').scrollIntoViewIfNeeded(); await screenshot('validation-mobile-dark');
  await page.getByRole('button', { name: 'Discard changes', exact: true }).click();
  await page.getByRole('link', { name: 'Board settings · beta', exact: true }).click(); await page.locator('[data-key="board_label"] input').waitFor(); await screenshot('board-mobile-dark');
  await page.getByRole('combobox', { name: 'Lane settings', exact: true }).selectOption('backlog'); await page.locator('[data-key="agent"]').waitFor(); await screenshot('lane-mobile-dark');
  // Unknown lanes, unavailable workspaces, removed plugins, and zero workspaces use real temporary config changes.
  await page.goto(sandbox.url + '/workspaces/beta/settings/plugins/statuses/missing'); await page.getByRole('alert').filter({ hasText: 'not a composed status' }).first().waitFor();
  assert(await page.getByRole('button', { name: 'Save', exact: true }).isDisabled(), 'Unknown lane is not silently converted to a writable scope');
  await writeFile(sandbox.configPath('beta'), betaGood.replace(/^[ \t]*board_label:.*\n/m, ''));
  await waitWorkspace('beta', 'unavailable');
  await page.goto(sandbox.url + '/workspaces/beta/settings/plugins'); await page.getByRole('alert').filter({ hasText: 'unavailable' }).first().waitFor();
  assert(await page.getByRole('link', { name: 'Instance settings', exact: true }).isVisible(), 'Unavailable boards still offer reachable instance settings');
  await page.getByRole('link', { name: 'Instance settings', exact: true }).click(); await page.locator('[data-key="greeting"] input').waitFor();
  await writeFile(sandbox.configPath('beta'), betaGood);
  const registered = await readFile(sandbox.registry, 'utf8');
  await writeFile(sandbox.registry, registered.slice(0, registered.indexOf('plugins:')) + 'plugins: []\n');
  await waitWorkspace('beta', 'unavailable', 'not installed');
  await page.goto(sandbox.url + '/workspaces/beta/settings/plugins');
  await page.getByRole('alert').filter({ hasText: 'not installed' }).first().waitFor();
  assert(true, 'An enabled but missing plugin is an operator-repair error, not an empty board');
  await writeFile(sandbox.registry, registered);
  await page.goto(sandbox.url + '/settings/plugins'); await page.locator('[data-key="greeting"] input').waitFor();
  await writeFile(sandbox.registry, 'workspaces: []\n' + registered.slice(registered.indexOf('plugins:')));
  await page.reload(); await page.locator('[data-key="greeting"] input').waitFor();
  assert(await page.locator('[data-key="greeting"] input').isEnabled(), 'Instance settings work with zero registered workspaces');
  await edit('greeting', 'removed-plugin-draft');
  await writeFile(sandbox.registry, 'workspaces: []\nplugins: []\n');
  await page.getByRole('button', { name: 'Reload configuration', exact: true }).click();
  await page.waitForFunction(() => document.querySelector<HTMLButtonElement>('.settings-actions button')?.disabled);
  assert(await page.locator('[data-key="greeting"] input').inputValue() === 'removed-plugin-draft', 'Removed plugin retains cached form instead of silently dropping state');
  await page.getByRole('button', { name: 'Discard changes', exact: true }).click();
  await page.reload(); await page.getByText('No plugins are installed on this instance.', { exact: true }).waitFor();
  assert(true, 'Empty catalogue has an explicit empty state');
  assert(!(await page.content()).includes(secretSentinel) && !bodies.join('').includes(secretSentinel) && !(await page.evaluate(() => JSON.stringify({ ...localStorage, ...sessionStorage }))).includes(secretSentinel), 'Environment secret sentinel never reaches DOM, requests or browser storage');
  assert(errors.length === 0, 'No browser runtime errors: ' + errors.join(', '));
  await writeFile(join(evidence, 'validation.txt'), passes.join('\n') + '\n');
  console.log(`Settings evidence: ${evidence}`);
} catch (cause) {
  await screenshot('failure');
  await writeFile(join(evidence, 'failure.txt'), String(cause) + '\n' + errors.join('\n') + '\n' + await page.locator('body').innerText());
  throw cause;
} finally { process.removeListener('SIGINT', interrupted); process.removeListener('SIGTERM', interrupted); await browser.close(); await sandbox.cleanup(); }
