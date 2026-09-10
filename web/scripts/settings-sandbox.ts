import { mkdtemp, mkdir, readFile, writeFile, cp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve, sep } from 'node:path';
import { spawn, type ChildProcess } from 'node:child_process';

const repository = resolve(new URL('../..', import.meta.url).pathname);
export const secretSentinel = 'SETTINGS-FIXTURE-SECRET-NEVER-RENDER';
export async function settingsSandbox() {
  const root = await mkdtemp(join(tmpdir(), 'docket-settings-'));
  const registry = join(root, 'registry.yaml');
  const plugin = join(root, 'plugin');
  const binary = join(root, 'docket');
  const env = Object.fromEntries(Object.entries(process.env).filter(([key]) => !/^(DOCKET_|DISPATCH_|PI_|CLAUDE_|CODEX_|GROK_)/.test(key)));
  Object.assign(env, { DOCKET_HOME: join(root, 'alpha'), DOCKET_CONFIG: registry, XDG_CONFIG_HOME: join(root, 'xdg-config'), XDG_DATA_HOME: join(root, 'xdg-data'), XDG_STATE_HOME: join(root, 'xdg-state'), DOCKET_ACTOR: 'settings-fixture', SETTINGS_FIXTURE_TOKEN: secretSentinel });
  for (const key of ['DOCKET_HOME', 'DOCKET_CONFIG', 'XDG_CONFIG_HOME', 'XDG_DATA_HOME', 'XDG_STATE_HOME']) if (!resolve(env[key]!).startsWith(root + sep)) throw new Error(`Unsafe sandbox ${key}`);
  let service: ChildProcess | undefined;
  const stop = async () => {
    if (service?.pid && service.exitCode === null && service.signalCode === null) {
      const exited = new Promise<void>((done) => service!.once('exit', () => done()));
      service.kill('SIGTERM');
      const timer = setTimeout(() => service?.kill('SIGKILL'), 5000);
      await exited; clearTimeout(timer);
    }
  };
  const cleanup = async () => { await stop(); await rm(root, { recursive: true, force: true }); };
  const run = (command: string, args: string[], cwd = root) => new Promise<string>((done, fail) => {
    if (cwd !== repository && !resolve(cwd).startsWith(root + sep) && cwd !== root) throw new Error('Unsafe CLI cwd');
    const child = spawn(command, args, { cwd, env, stdio: ['ignore', 'pipe', 'pipe'] });
    let output = ''; child.stdout.on('data', (data) => { output += data; }); child.stderr.on('data', (data) => { output += data; });
    child.once('error', fail); child.once('exit', (code) => code === 0 ? done(output) : fail(new Error(`${command} ${args.join(' ')} failed (${code}): ${output}`)));
  });
  try {
    await writeFile(registry, 'workspaces: []\nplugins: []\n');
    await cp(join(repository, 'web/tests/fixtures/plugin-settings'), plugin, { recursive: true });
    await run('go', ['build', '-o', binary, '.'], repository);
    for (const name of ['alpha', 'beta']) {
      const cwd = join(root, name); await mkdir(cwd);
      await run(binary, ['init'], cwd);
      const path = join(cwd, '.docket/config.yaml');
      await writeFile(path, await readFile(path, 'utf8') + `plugins:\n  settings-fixture:\n    config: {board_label: ${name}}\n`);
    }
    const config = await readFile(registry, 'utf8');
    await writeFile(registry, config.replace(/plugins: \[\]\n?/, '') + `\nplugins:\n  - name: settings-fixture\n    path: ${plugin}\n    source: {type: local}\n    version: 1.2.0\n`);
    for (const name of ['alpha', 'beta']) await run(binary, ['workspace', 'check', join(root, name)]);
    service = spawn(binary, ['serve', '--all', '--listen', '127.0.0.1:0'], { cwd: root, env, stdio: ['ignore', 'pipe', 'pipe'] });
    let log = '';
    const url = await new Promise<string>((done, fail) => {
      const timer = setTimeout(() => fail(new Error(`Sandbox service startup timed out: ${log}`)), 15000);
      const inspect = (data: Buffer) => {
        log += data.toString();
        const match = log.match(/http:\/\/127\.0\.0\.1:\d+/);
        if (match) { clearTimeout(timer); done(match[0]); }
      };
      service!.stdout!.on('data', inspect); service!.stderr!.on('data', inspect);
      service!.once('error', (cause) => { clearTimeout(timer); fail(cause); }); service!.once('exit', (code) => { clearTimeout(timer); fail(new Error(`Sandbox service exited (${code}): ${log}`)); });
    });
    if (!/^http:\/\/127\.0\.0\.1:\d+$/.test(url)) throw new Error('Unsafe sandbox URL');
    const request = async (path: string, options?: RequestInit) => {
      if (!path.startsWith('/') || path.startsWith('//')) throw new Error('Only sandbox-relative requests are allowed');
      return fetch(url + path, options);
    };
    for (let attempt = 0; attempt < 100; attempt++) {
      const statuses = await (await request('/api/workspaces')).json() as { state: string }[];
      if (statuses.length === 2 && statuses.every((item) => item.state === 'watching')) break;
      if (attempt === 99) throw new Error('Fixture workspaces did not start');
      await Bun.sleep(100);
    }
    console.log(`Isolated settings sandbox: ${root}\nPreview: ${url}/settings/plugins`);
    return { root, registry, plugin, url, request, stop, cleanup, run, binary, configPath: (name: 'alpha' | 'beta') => join(root, name, '.docket/config.yaml') };
  } catch (cause) { await cleanup(); throw cause; }
}
