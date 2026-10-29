import { readFileSync } from 'node:fs';

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type * as vscode from 'vscode';

import {
  changeActiveEditor,
  changeConfiguration,
  FakeEditor,
  memento,
  commands,
  decorationTypes,
  quickPicks,
  settingUpdates,
  visibleEditors,
  reset,
  runCommand,
  settings,
  shownErrors,
  statusBarItems,
} from '../test/vscode';
import { activate, deactivate } from './extension';
import { CHOOSE_COLOR_STYLE_COMMAND } from './color_style';
import { PICK_VERSION_COMMAND, RESTART_SERVER_COMMAND } from './kubernetes_version';

const client = vi.hoisted(() => ({ notifications: [] as { method: string; params: unknown }[] }));

vi.mock('vscode-languageclient/node', () => ({
  TransportKind: { stdio: 0 },
  LanguageClient: class {
    get initializeResult(): unknown {
      return {
        capabilities: {
          experimental: {
            celLens: { kubernetesVersion: '1.37', supportedKubernetesVersions: ['1.37', '1.36'] },
          },
        },
      };
    }

    sendNotification(method: string, params: unknown): Promise<void> {
      client.notifications.push({ method, params });
      return Promise.resolve();
    }

    start(): Promise<void> {
      return Promise.resolve();
    }

    stop(): Promise<void> {
      return Promise.resolve();
    }
  },
}));

interface Manifest {
  repository: { url: string };
  homepage: string;
  bugs: { url: string };
  activationEvents: string[];
  contributes: {
    commands: { command: string; title: string; category: string }[];
    configuration: {
      properties: Record<
        string,
        { default?: unknown; markdownDescription?: string; scope?: string }
      >;
    };
  };
}

const manifest = JSON.parse(
  readFileSync(new URL('../package.json', import.meta.url), 'utf8'),
) as Manifest;

const CRD = 'x-kubernetes-validations:\n  - rule: "self.a < 3"\n';

describe('activate', () => {
  beforeEach(() => {
    client.notifications.length = 0;
    vi.stubEnv('PATH', '/nowhere');
  });
  afterEach(async () => {
    // Waits for the server start the test began, so it cannot show an error in the next test.
    await deactivate();
    reset();
    vi.unstubAllEnvs();
  });

  it('registers every command package.json contributes', async () => {
    activate({ subscriptions: [], extensionPath: '/ext', globalState: memento() });

    const registered = await commands.getCommands();
    for (const { command } of manifest.contributes.commands) {
      expect(registered).toContain(command);
    }
  });

  it('registers the restart command', async () => {
    activate({ subscriptions: [], extensionPath: '/ext', globalState: memento() });

    expect(await commands.getCommands()).toContain(RESTART_SERVER_COMMAND);
  });

  it('restarts the server when the restart command runs', async () => {
    activate({ subscriptions: [], extensionPath: '/ext', globalState: memento() });
    await vi.waitFor(() => {
      expect(shownErrors.length).toBeGreaterThan(0);
    });
    const before = shownErrors.length;

    await runCommand(RESTART_SERVER_COMMAND);

    expect(shownErrors).toHaveLength(before + 1);
  });

  it('activates on YAML files only', () => {
    expect(manifest.activationEvents).toEqual(['onLanguage:yaml']);
  });

  it('registers the pick version command', async () => {
    activate({ subscriptions: [], extensionPath: '/ext', globalState: memento() });

    expect(await commands.getCommands()).toContain(PICK_VERSION_COMMAND);
  });

  it('sends a version change to the running server and updates the status text', async () => {
    settings.set('cel-lens.serverPath', process.execPath);
    changeActiveEditor('yaml');
    activate({ subscriptions: [], extensionPath: '/ext', globalState: memento() });
    await vi.waitFor(() => {
      expect(statusBarItems[0]?.text).toBe('CEL · K8s 1.37');
    });

    settings.set('cel-lens.kubernetesVersion', '1.36');
    changeConfiguration('cel-lens.kubernetesVersion');

    await vi.waitFor(() => {
      expect(client.notifications).toEqual([
        {
          method: 'workspace/didChangeConfiguration',
          params: { settings: { kubernetesVersion: '1.36', experimental: [] } },
        },
      ]);
    });
    expect(statusBarItems[0]?.text).toBe('CEL · K8s 1.36');
  });

  it('sends an experimental list change to the running server', async () => {
    settings.set('cel-lens.serverPath', process.execPath);
    changeActiveEditor('yaml');
    activate({ subscriptions: [], extensionPath: '/ext', globalState: memento() });
    await vi.waitFor(() => {
      expect(statusBarItems[0]?.text).toBe('CEL · K8s 1.37');
    });

    settings.set('cel-lens.experimental', ['kyverno']);
    changeConfiguration('cel-lens.experimental');

    await vi.waitFor(() => {
      expect(client.notifications).toEqual([
        {
          method: 'workspace/didChangeConfiguration',
          params: { settings: { kubernetesVersion: '', experimental: ['kyverno'] } },
        },
      ]);
    });
  });

  it('contributes the experimental setting as an empty list of the three known names', () => {
    const setting = manifest.contributes.configuration.properties['cel-lens.experimental'];

    expect(setting).toMatchObject({
      type: 'array',
      default: [],
      items: {
        type: 'string',
        enum: ['kyverno', 'crd-typed-policies', 'fitting-completion'],
        enumDescriptions: [expect.any(String), expect.any(String), expect.any(String)],
      },
    });
  });

  it('sends nothing when another setting changes', async () => {
    settings.set('cel-lens.serverPath', process.execPath);
    changeActiveEditor('yaml');
    activate({ subscriptions: [], extensionPath: '/ext', globalState: memento() });
    await vi.waitFor(() => {
      expect(statusBarItems[0]?.text).toBe('CEL · K8s 1.37');
    });

    changeConfiguration('cel-lens.trace.server');

    expect(client.notifications).toEqual([]);
  });

  it('contributes the version setting empty by default, marked advanced', () => {
    const setting = manifest.contributes.configuration.properties['cel-lens.kubernetesVersion'];

    expect(setting).toMatchObject({ default: '' });
    expect(setting?.markdownDescription).toMatch(/^Advanced/);
  });

  it.each(['cel-lens.serverPath', 'cel-lens.trace.server', 'cel-lens.kubernetesVersion'])(
    'contributes the %s setting with a default and a description',
    (name) => {
      const setting = manifest.contributes.configuration.properties[name];

      expect(setting?.default).toBeDefined();
      expect(setting?.markdownDescription).toBeTruthy();
    },
  );

  it('contributes the color style setting with its three values and cel as the default', () => {
    const setting = manifest.contributes.configuration.properties['cel-lens.colorStyle'];

    expect(setting).toMatchObject({
      default: 'cel',
      enum: ['cel', 'classic', 'off'],
      enumDescriptions: [expect.any(String), expect.any(String), expect.any(String)],
    });
    expect(setting?.markdownDescription).toBeTruthy();
  });

  it('contributes the choose color style command under the cel-lens category', () => {
    const command = manifest.contributes.commands.find(
      (c) => c.command === CHOOSE_COLOR_STYLE_COMMAND,
    );

    expect(command).toMatchObject({ title: 'Choose Color Style', category: 'cel-lens' });
  });

  it('runs the color style quick pick from the command', async () => {
    activate({ subscriptions: [], extensionPath: '/ext', globalState: memento() });
    quickPicks.next = 'Classic';

    await runCommand(CHOOSE_COLOR_STYLE_COMMAND);

    expect(settingUpdates).toMatchObject([{ name: 'cel-lens.colorStyle', value: 'classic' }]);
  });

  it('paints on activation and removes the decorations when it is disposed', () => {
    const editor = new FakeEditor({ languageId: 'yaml', getText: () => CRD });
    visibleEditors.push(editor);
    const subscriptions: vscode.Disposable[] = [];
    activate({ subscriptions, extensionPath: '/ext', globalState: memento() });
    expect(decorationTypes.length).toBeGreaterThan(0);

    for (const subscription of subscriptions) {
      subscription.dispose();
    }

    expect(decorationTypes.every((type) => type.disposed)).toBe(true);
  });

  it('contributes the restart command under the cel-lens category', () => {
    const command = manifest.contributes.commands.find((c) => c.command === RESTART_SERVER_COMMAND);

    expect(command).toMatchObject({ title: 'Restart Engine', category: 'cel-lens' });
  });

  it('hands every registration to VS Code to dispose', async () => {
    const subscriptions: vscode.Disposable[] = [];
    activate({ subscriptions, extensionPath: '/ext', globalState: memento() });

    for (const subscription of subscriptions) {
      subscription.dispose();
    }

    expect(await commands.getCommands()).toEqual([]);
  });

  it('registers no command that package.json does not contribute', async () => {
    activate({ subscriptions: [], extensionPath: '/ext', globalState: memento() });

    const contributed = manifest.contributes.commands.map((c) => c.command);
    for (const registered of await commands.getCommands()) {
      expect(contributed).toContain(registered);
    }
  });
});

describe('published text', () => {
  const readme = readFileSync(new URL('../README.md', import.meta.url), 'utf8');
  const guide = readFileSync(new URL('../CONTRIBUTING.md', import.meta.url), 'utf8');
  const repository = 'github.com/JairJosafath/cel-lens';

  it.each([
    ['repository', manifest.repository.url],
    ['homepage', manifest.homepage],
    ['bugs', manifest.bugs.url],
  ])('points %s at the cel-lens repository', (_field, url) => {
    expect(url).toContain(repository);
  });

  it('asks for no binary to install and no path to set', () => {
    expect(readme).not.toContain('cel-lens/cel-lens-core');
    expect(readme).not.toMatch(/install `cel-lens-core`|on your `PATH`|put it on/i);
  });

  it('names the cel-lens-core release the extension is built for', () => {
    expect(guide).toContain('cel-lens-core` v0.17.2');
  });

  it('shows no message the engine does not give', () => {
    expect(readme).not.toContain('no such key');
  });

  it('names the color styles by their picker labels in the Choose Color Style entry and by values in the setting row', () => {
    const entry =
      guide.slice(guide.indexOf('- **cel-lens: Choose Color Style**')).split('\n- ')[0] ?? '';
    expect(entry).toContain('CEL, Classic or Off');
    expect(entry).not.toContain('`classic`');
    expect(readme).toMatch(/\| `cel-lens\.colorStyle`[^\n]*`cel`, `classic` or `off`/);
  });

  it('lists the version setting in the README and the command in the guide', () => {
    expect(readme).toContain('cel-lens.kubernetesVersion');
    expect(guide).toContain('cel-lens.pickKubernetesVersion');
  });

  it('has a Color styles section with the setting, the command and every style', () => {
    const section = guide.slice(guide.indexOf('### Color styles'));

    for (const text of [
      'cel-lens.colorStyle',
      CHOOSE_COLOR_STYLE_COMMAND,
      '`cel`',
      '`classic`',
      '`off`',
    ]) {
      expect(section).toContain(text);
    }
    expect(section).toMatch(
      /\| Part +\| Light +\| Dark +\| High contrast dark +\| High contrast light +\|/,
    );
  });

  it.each([
    'user settings',
    'workspace setting',
    "folder setting of the open file's workspace folder",
    '`.code-workspace` file',
  ])('says in the Color styles section which scope a pick writes: %s', (text) => {
    const section = guide.slice(guide.indexOf('### Color styles'));
    expect(section.replace(/\s+/g, ' ')).toContain(text);
  });

  it('keeps no template leftovers', () => {
    expect(JSON.stringify(manifest)).not.toMatch(/sayHello|Say Hello/);
  });
});
