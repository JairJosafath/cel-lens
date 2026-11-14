import { readFileSync } from 'node:fs';

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type * as vscode from 'vscode';

import {
  ConfigurationTarget,
  changeActiveEditor,
  changeConfiguration,
  FakeEditor,
  memento,
  commands,
  decorationTypes,
  quickPicks,
  settingUpdates,
  executedCommands,
  shownDocuments,
  visibleEditors,
  reset,
  runCommand,
  settings,
  shownErrors,
  statusBarItems,
} from '../test/vscode';
import { activate, deactivate } from './extension';
import { CHOOSE_COLOR_STYLE_COMMAND } from './color_style';
import { SHOW_EXAMPLE_RESULTS_COMMAND, TOGGLE_EXAMPLE_RESULTS_COMMAND } from './example_results';
import { TOGGLE_INLINE_INFO_COMMAND } from './inline_info';
import { LIST_VALUES_COMMAND, SHOW_RULE_COMMAND } from './rule_parts';
import { GIVE_TYPE_COMMAND, GIVE_VALUE_TYPE_COMMAND, REMOVE_TYPE_COMMAND } from './value_types';
import { PICK_VERSION_COMMAND, RESTART_SERVER_COMMAND } from './kubernetes_version';

const client = vi.hoisted(() => ({
  notifications: [] as { method: string; params: unknown }[],
  requests: [] as { method: string; params: unknown }[],
}));

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

    onNotification(): void {
      return undefined;
    }

    onRequest(): void {
      return undefined;
    }

    getFeature(): undefined {
      return undefined;
    }

    sendNotification(method: string, params: unknown): Promise<void> {
      client.notifications.push({ method, params });
      return Promise.resolve();
    }

    /** The engine applies every edit it is asked for. */
    sendRequest(method: string, params: unknown): Promise<unknown> {
      client.requests.push({ method, params });
      return Promise.resolve({ cellens: 'file:///workspace/.cellens', change: 'added' });
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
    menus: { commandPalette: { command: string; when: string }[] };
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
    client.requests.length = 0;
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

  it('activates on YAML files, and on the in-memory files of pasted examples a reload restores (#14)', () => {
    expect(manifest.activationEvents).toEqual(['onLanguage:yaml', 'onFileSystem:cel-lens-example']);
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
          params: {
            settings: {
              kubernetesVersion: '1.36',
              experimental: [],
              inlineInfo: true,
              exampleResults: true,
            },
          },
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
          params: {
            settings: {
              kubernetesVersion: '',
              experimental: ['kyverno'],
              inlineInfo: true,
              exampleResults: true,
            },
          },
        },
      ]);
    });
  });

  it('sends the inline info setting to the running server when it changes', async () => {
    settings.set('cel-lens.serverPath', process.execPath);
    changeActiveEditor('yaml');
    activate({ subscriptions: [], extensionPath: '/ext', globalState: memento() });
    await vi.waitFor(() => {
      expect(statusBarItems[0]?.text).toBe('CEL · K8s 1.37');
    });

    settings.set('cel-lens.inlineInfo', false);
    changeConfiguration('cel-lens.inlineInfo');

    await vi.waitFor(() => {
      expect(client.notifications).toEqual([
        {
          method: 'workspace/didChangeConfiguration',
          params: {
            settings: {
              kubernetesVersion: '',
              experimental: [],
              inlineInfo: false,
              exampleResults: true,
            },
          },
        },
      ]);
    });
  });

  it('contributes the inline info setting as a boolean, on by default, with resource scope', () => {
    const setting = manifest.contributes.configuration.properties['cel-lens.inlineInfo'];

    expect(setting).toMatchObject({ type: 'boolean', default: true, scope: 'resource' });
    expect(setting?.markdownDescription).toContain('cel-lens: Toggle Inline Info');
  });

  it('contributes the toggle inline info command under the cel-lens category, in the palette in YAML', () => {
    const command = manifest.contributes.commands.find(
      (c) => c.command === TOGGLE_INLINE_INFO_COMMAND,
    );
    const palette = manifest.contributes.menus.commandPalette.find(
      (entry) => entry.command === TOGGLE_INLINE_INFO_COMMAND,
    );

    expect(command).toMatchObject({ title: 'Toggle Inline Info', category: 'cel-lens' });
    expect(palette?.when).toBe('editorLangId == yaml');
  });

  it('turns the lines off in the user settings from the toggle command', async () => {
    activate({ subscriptions: [], extensionPath: '/ext', globalState: memento() });

    await runCommand(TOGGLE_INLINE_INFO_COMMAND);

    expect(settingUpdates).toEqual([
      { name: 'cel-lens.inlineInfo', value: false, target: ConfigurationTarget.Global },
    ]);
  });

  it('sends the example results setting to the running server when it changes', async () => {
    settings.set('cel-lens.serverPath', process.execPath);
    changeActiveEditor('yaml');
    activate({ subscriptions: [], extensionPath: '/ext', globalState: memento() });
    await vi.waitFor(() => {
      expect(statusBarItems[0]?.text).toBe('CEL · K8s 1.37');
    });

    settings.set('cel-lens.exampleResults', false);
    changeConfiguration('cel-lens.exampleResults');

    await vi.waitFor(() => {
      expect(client.notifications).toEqual([
        {
          method: 'workspace/didChangeConfiguration',
          params: {
            settings: {
              kubernetesVersion: '',
              experimental: [],
              inlineInfo: true,
              exampleResults: false,
            },
          },
        },
      ]);
    });
  });

  it('turns example results off in the user settings from the toggle command', async () => {
    activate({ subscriptions: [], extensionPath: '/ext', globalState: memento() });

    await runCommand(TOGGLE_EXAMPLE_RESULTS_COMMAND);

    expect(settingUpdates).toEqual([
      { name: 'cel-lens.exampleResults', value: false, target: ConfigurationTarget.Global },
    ]);
  });

  it('asks the engine for the results of the rule when the part of its line runs, and shows them in a picker', async () => {
    settings.set('cel-lens.serverPath', process.execPath);
    changeActiveEditor('yaml');
    activate({ subscriptions: [], extensionPath: '/ext', globalState: memento() });
    await vi.waitFor(() => {
      expect(statusBarItems[0]?.text).toBe('CEL · K8s 1.37');
    });
    const rule = {
      uri: 'file:///workspace/crd.yaml',
      range: { start: { line: 15, character: 30 }, end: { line: 15, character: 52 } },
    };

    await runCommand(SHOW_EXAMPLE_RESULTS_COMMAND, rule);

    expect(client.requests).toEqual([
      {
        method: 'workspace/executeCommand',
        params: { command: 'cel-lens.exampleResults', arguments: [rule] },
      },
    ]);
    expect(quickPicks.created).toMatchObject([{ title: 'Example results of the rule on line 16' }]);
  });

  it('contributes the experimental setting as an empty list of the four known names', () => {
    const setting = manifest.contributes.configuration.properties['cel-lens.experimental'];

    expect(setting).toMatchObject({
      type: 'array',
      default: [],
      items: {
        type: 'string',
        enum: ['kyverno', 'crd-typed-policies', 'fitting-completion', 'cellens-types'],
        enumDescriptions: [
          expect.any(String),
          expect.any(String),
          expect.any(String),
          expect.any(String),
        ],
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

  it('registers the commands of the parts of the line once, and frees them on dispose for the next activation', async () => {
    const subscriptions: vscode.Disposable[] = [];
    activate({ subscriptions, extensionPath: '/ext', globalState: memento() });
    expect(await commands.getCommands()).toEqual(
      expect.arrayContaining([
        SHOW_RULE_COMMAND,
        LIST_VALUES_COMMAND,
        SHOW_EXAMPLE_RESULTS_COMMAND,
      ]),
    );

    for (const subscription of subscriptions) {
      subscription.dispose();
    }
    await deactivate();

    expect(() => {
      activate({ subscriptions: [], extensionPath: '/ext', globalState: memento() });
    }).not.toThrow();
  });

  it('shows the hover of a rule with no problem when its summary part runs', async () => {
    activate({ subscriptions: [], extensionPath: '/ext', globalState: memento() });
    const range = { start: { line: 1, character: 10 }, end: { line: 1, character: 20 } };

    await runCommand(SHOW_RULE_COMMAND, { uri: 'file:///workspace/crd.yaml', range });

    expect(shownDocuments).toMatchObject([{ uri: 'file:///workspace/crd.yaml' }]);
    expect(executedCommands).toEqual([
      { command: 'closeMarkersNavigation', args: [] },
      { command: 'editor.action.showHover', args: [] },
    ]);
  });

  it('lists the values a line leaves out when its +N more part runs, and runs the one picked', async () => {
    activate({ subscriptions: [], extensionPath: '/ext', globalState: memento() });
    const target = { path: 'spec.port' };
    quickPicks.next = 'self.spec.port: dyn';

    await runCommand(LIST_VALUES_COMMAND, {
      line: 16,
      parts: [
        {
          title: 'self.spec.port: dyn · Give it a type',
          command: 'cel-lens.giveType',
          arguments: [target],
        },
      ],
    });

    expect(executedCommands).toEqual([{ command: 'cel-lens.giveType', args: [target] }]);
  });

  it('gives a value of a CRD a type from its Give it a type part, through the engine', async () => {
    settings.set('cel-lens.serverPath', process.execPath);
    changeActiveEditor('yaml');
    activate({ subscriptions: [], extensionPath: '/ext', globalState: memento() });
    await vi.waitFor(() => {
      expect(statusBarItems[0]?.text).toBe('CEL · K8s 1.37');
    });
    const surge = {
      cellens: 'file:///workspace/.cellens',
      file: 'crd.yaml',
      document: 'CustomResourceDefinition widgets.example.com',
      version: 'v1',
      path: 'spec.maxSurge',
      value: 'self.spec.maxSurge',
    };
    quickPicks.next = '$(symbol-string) string';

    await runCommand(GIVE_TYPE_COMMAND, surge);
    await runCommand(REMOVE_TYPE_COMMAND, { ...surge, type: 'string' });

    expect(client.requests).toEqual([
      {
        method: 'workspace/executeCommand',
        params: { command: 'cel-lens.setType', arguments: [{ ...surge, type: 'string' }] },
      },
      {
        method: 'workspace/executeCommand',
        params: { command: 'cel-lens.removeType', arguments: [{ ...surge, type: 'string' }] },
      },
    ]);
    expect(shownErrors).toEqual([]);
  });

  it('contributes Give a Value a Type… under the cel-lens category, in the palette in YAML only', () => {
    const command = manifest.contributes.commands.find(
      (c) => c.command === GIVE_VALUE_TYPE_COMMAND,
    );
    const palette = manifest.contributes.menus.commandPalette.find(
      (entry) => entry.command === GIVE_VALUE_TYPE_COMMAND,
    );

    expect(command).toMatchObject({ title: 'Give a Value a Type…', category: 'cel-lens' });
    expect(palette?.when).toBe('editorLangId == yaml');
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
    expect(guide).toContain('cel-lens-core` v0.32.0');
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
