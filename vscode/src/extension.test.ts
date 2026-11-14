import { readFileSync } from 'node:fs';

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type * as vscode from 'vscode';

import {
  Uri,
  activeEditor,
  moveCursor,
  statusBarMessages,
  addWorkspaceFolder,
  ConfigurationTarget,
  changeActiveEditor,
  changeConfiguration,
  contextKeys,
  FakeEditor,
  memento,
  commands,
  decorationTypes,
  quickPicks,
  settingUpdates,
  executedCommands,
  openedByApi,
  openedUrls,
  settle,
  shownInfos,
  shownWarnings,
  writtenFiles,
  shownDocuments,
  visibleEditors,
  reset,
  runCommand,
  settings,
  shownErrors,
  statusBarItems,
  treeViews,
  channels,
  progressRuns,
  ProgressLocation,
} from '../test/vscode';
import { activate, deactivate } from './extension';
import { CHOOSE_COLOR_STYLE_COMMAND, TOGGLE_COLORS_COMMAND } from './color_style';
import { GET_STARTED_COMMAND, OPEN_SAMPLE_COMMAND } from './get_started';
import { SHOW_EXAMPLE_RESULTS_COMMAND, TOGGLE_EXAMPLE_RESULTS_COMMAND } from './example_results';
import { TOGGLE_INLINE_INFO_COMMAND } from './inline_info';
import { TOGGLE_VALUE_TYPES_COMMAND } from './value_visibility';
import { LIST_VALUES_COMMAND, SHOW_RULE_COMMAND } from './rule_parts';
import { GIVE_TYPE_COMMAND, GIVE_VALUE_TYPE_COMMAND, REMOVE_TYPE_COMMAND } from './value_types';
import { PICK_VERSION_COMMAND, RESTART_SERVER_COMMAND } from './kubernetes_version';

const client = vi.hoisted(() => ({
  notifications: [] as { method: string; params: unknown }[],
  requests: [] as { method: string; params: unknown }[],
  handlers: new Map<string, (params: unknown) => void>(),
  answers: new Map<string, unknown>(),
  /** How often the code lens provider was told to ask for the lines again. */
  lensRefreshes: 0,
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

    onNotification(method: string, handler: (params: unknown) => void): void {
      client.handlers.set(method, handler);
    }

    onRequest(): void {
      return undefined;
    }

    getFeature(): unknown {
      return {
        getProvider: () => ({
          onDidChangeCodeLensEmitter: {
            fire: () => {
              client.lensRefreshes += 1;
            },
          },
        }),
      };
    }

    sendNotification(method: string, params: unknown): Promise<void> {
      client.notifications.push({ method, params });
      return Promise.resolve();
    }

    /** The engine applies every edit it is asked for. */
    sendRequest(method: string, params: unknown): Promise<unknown> {
      client.requests.push({ method, params });
      const command =
        typeof params === 'object' && params !== null && 'command' in params
          ? String(params.command)
          : method;
      return Promise.resolve(
        client.answers.get(command) ?? { cellens: 'file:///workspace/.cellens', change: 'added' },
      );
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
    keybindings?: { command: string; key: string; mac?: string; when?: string }[];
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

/** The requests the engine got besides the view's asks for the list of the rules. */
function requestsButTheList(): { method: string; params: unknown }[] {
  return client.requests.filter(
    (request) =>
      !(
        typeof request.params === 'object' &&
        request.params !== null &&
        'command' in request.params &&
        request.params.command === 'cel-lens.ruleList'
      ),
  );
}

const CRD = 'x-kubernetes-validations:\n  - rule: "self.a < 3"\n';

describe('activate', () => {
  beforeEach(() => {
    client.notifications.length = 0;
    client.requests.length = 0;
    client.handlers.clear();
    client.answers.clear();
    client.lensRefreshes = 0;
    vi.stubEnv('PATH', '/nowhere');
  });
  afterEach(async () => {
    // Waits for the server start the test began, so it cannot show an error in the next test.
    await deactivate();
    reset();
    vi.unstubAllEnvs();
  });

  it('registers every command package.json contributes', async () => {
    activate({
      subscriptions: [],
      extensionPath: '/ext',
      globalStorageUri: Uri.parse('file:///storage'),
      globalState: memento(),
      workspaceState: memento(),
    });

    const registered = await commands.getCommands();
    for (const { command } of manifest.contributes.commands) {
      expect(registered).toContain(command);
    }
  });

  it('registers the restart command', async () => {
    activate({
      subscriptions: [],
      extensionPath: '/ext',
      globalStorageUri: Uri.parse('file:///storage'),
      globalState: memento(),
      workspaceState: memento(),
    });

    expect(await commands.getCommands()).toContain(RESTART_SERVER_COMMAND);
  });

  it('restarts the server when the restart command runs', async () => {
    activate({
      subscriptions: [],
      extensionPath: '/ext',
      globalStorageUri: Uri.parse('file:///storage'),
      globalState: memento(),
      workspaceState: memento(),
    });
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

  it('opens nothing by itself on activation, as on install or update: no walkthrough, no document, no link, no notification', async () => {
    settings.set('cel-lens.serverPath', process.execPath);
    activate({
      subscriptions: [],
      extensionPath: '/ext',
      globalStorageUri: Uri.parse('file:///storage'),
      globalState: memento(),
      workspaceState: memento(),
    });
    await vi.waitFor(() => {
      expect(client.handlers.size).toBeGreaterThan(0);
    });
    await settle();

    // No command at all: only setContext, which the stand-in keeps in contextKeys instead. A
    // webview panel or a terminal would throw here, as the stand-in has neither.
    expect(executedCommands).toEqual([]);
    // Nor the Output panel, nor a notification's progress.
    expect(channels.get('cel-lens')?.shown).toBe(false);
    expect(progressRuns.map((run) => run.options.location)).not.toContain(
      ProgressLocation.Notification,
    );
    expect(shownDocuments).toEqual([]);
    expect(openedByApi).toEqual([]);
    expect(openedUrls).toEqual([]);
    expect(writtenFiles).toEqual([]);
    expect(shownErrors).toEqual([]);
    expect(shownWarnings).toEqual([]);
    expect(shownInfos).toEqual([]);
    expect(quickPicks.shown).toEqual([]);
    expect(manifest.activationEvents).toEqual(['onLanguage:yaml', 'onFileSystem:cel-lens-example']);
  });

  it('opens the walkthrough from Get Started', async () => {
    activate({
      subscriptions: [],
      extensionPath: '/ext',
      globalStorageUri: Uri.parse('file:///storage'),
      globalState: memento(),
      workspaceState: memento(),
    });

    await runCommand(GET_STARTED_COMMAND);

    expect(executedCommands).toContainEqual({
      command: 'workbench.action.openWalkthrough',
      args: ['cel-lens.cel-lens-vscode#getStarted'],
    });
  });

  it('writes the sample to the extension’s storage and shows it from Open the Sample CRD', async () => {
    activate({
      subscriptions: [],
      extensionPath: '/ext',
      globalStorageUri: Uri.parse('file:///storage'),
      globalState: memento(),
      workspaceState: memento(),
    });

    await runCommand(OPEN_SAMPLE_COMMAND);

    expect(writtenFiles).toEqual(['file:///storage/get-started/widgets.yaml']);
    expect(shownDocuments.map(({ uri }) => uri)).toEqual([
      'file:///storage/get-started/widgets.yaml',
    ]);
  });

  it('registers the pick version command', async () => {
    activate({
      subscriptions: [],
      extensionPath: '/ext',
      globalStorageUri: Uri.parse('file:///storage'),
      globalState: memento(),
      workspaceState: memento(),
    });

    expect(await commands.getCommands()).toContain(PICK_VERSION_COMMAND);
  });

  it('sends a version change to the running server and updates the status text', async () => {
    settings.set('cel-lens.serverPath', process.execPath);
    changeActiveEditor('yaml');
    activate({
      subscriptions: [],
      extensionPath: '/ext',
      globalStorageUri: Uri.parse('file:///storage'),
      globalState: memento(),
      workspaceState: memento(),
    });
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
    activate({
      subscriptions: [],
      extensionPath: '/ext',
      globalStorageUri: Uri.parse('file:///storage'),
      globalState: memento(),
      workspaceState: memento(),
    });
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
    activate({
      subscriptions: [],
      extensionPath: '/ext',
      globalStorageUri: Uri.parse('file:///storage'),
      globalState: memento(),
      workspaceState: memento(),
    });
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
    activate({
      subscriptions: [],
      extensionPath: '/ext',
      globalStorageUri: Uri.parse('file:///storage'),
      globalState: memento(),
      workspaceState: memento(),
    });

    await runCommand(TOGGLE_INLINE_INFO_COMMAND);

    expect(settingUpdates).toEqual([
      { name: 'cel-lens.inlineInfo', value: false, target: ConfigurationTarget.Global },
    ]);
  });

  it('has VS Code ask for the lines again when valueTypes changes, and tells the engine nothing new', async () => {
    settings.set('cel-lens.serverPath', process.execPath);
    changeActiveEditor('yaml');
    activate({
      subscriptions: [],
      extensionPath: '/ext',
      globalStorageUri: Uri.parse('file:///storage'),
      globalState: memento(),
      workspaceState: memento(),
    });
    await vi.waitFor(() => {
      expect(statusBarItems[0]?.text).toBe('CEL · K8s 1.37');
    });
    visibleEditors.push(new FakeEditor({ languageId: 'yaml', getText: () => CRD }));

    settings.set('cel-lens.valueTypes', false);
    changeConfiguration('cel-lens.valueTypes');

    await vi.waitFor(() => {
      expect(client.lensRefreshes).toBe(1);
    });
    expect(client.notifications).toEqual([
      {
        method: 'workspace/didChangeConfiguration',
        params: {
          settings: {
            kubernetesVersion: '',
            experimental: [],
            inlineInfo: true,
            exampleResults: true,
          },
        },
      },
    ]);
    expect(requestsButTheList()).toEqual([]);
  });

  it('turns the values off in the user settings from the Toggle Value Types command', async () => {
    activate({
      subscriptions: [],
      extensionPath: '/ext',
      globalStorageUri: Uri.parse('file:///storage'),
      globalState: memento(),
      workspaceState: memento(),
    });

    await runCommand(TOGGLE_VALUE_TYPES_COMMAND);

    expect(settingUpdates).toEqual([
      { name: 'cel-lens.valueTypes', value: false, target: ConfigurationTarget.Global },
    ]);
  });

  it('turns the colors off in the user settings from the Toggle Colors command, and keeps the style it turned off', async () => {
    const state = memento();
    activate({
      subscriptions: [],
      extensionPath: '/ext',
      globalStorageUri: Uri.parse('file:///storage'),
      globalState: state,
      workspaceState: memento(),
    });
    settings.set('cel-lens.colorStyle', 'classic');

    await runCommand(TOGGLE_COLORS_COMMAND);

    expect(settingUpdates).toEqual([
      { name: 'cel-lens.colorStyle', value: 'off', target: ConfigurationTarget.Global },
    ]);
    expect(state.get('cel-lens.colorStyleBeforeOff')).toBe('classic');
  });

  it('sets the context key cel-lens.cellensTypes at activation, and again on each change of cel-lens.experimental', async () => {
    activate({
      subscriptions: [],
      extensionPath: '/ext',
      globalStorageUri: Uri.parse('file:///storage'),
      globalState: memento(),
      workspaceState: memento(),
    });
    await vi.waitFor(() => {
      expect(contextKeys.get('cel-lens.cellensTypes')).toBe(false);
    });

    settings.set('cel-lens.experimental', ['kyverno', 'cellens-types']);
    changeConfiguration('cel-lens.experimental');
    await vi.waitFor(() => {
      expect(contextKeys.get('cel-lens.cellensTypes')).toBe(true);
    });

    settings.set('cel-lens.experimental', ['kyverno']);
    changeConfiguration('cel-lens.experimental');
    await vi.waitFor(() => {
      expect(contextKeys.get('cel-lens.cellensTypes')).toBe(false);
    });
  });

  it('sets the context key cel-lens.cellensTypes to true at activation when cellens-types is listed', async () => {
    settings.set('cel-lens.experimental', ['cellens-types']);

    activate({
      subscriptions: [],
      extensionPath: '/ext',
      globalStorageUri: Uri.parse('file:///storage'),
      globalState: memento(),
      workspaceState: memento(),
    });

    await vi.waitFor(() => {
      expect(contextKeys.get('cel-lens.cellensTypes')).toBe(true);
    });
  });

  it('runs the one handler of Toggle Inline Info from its keybinding, Ctrl+K L (#38)', async () => {
    const [binding, ...others] = manifest.contributes.keybindings ?? [];
    expect(others).toEqual([]);
    expect(binding?.command).toBe(TOGGLE_INLINE_INFO_COMMAND);
    activate({
      subscriptions: [],
      extensionPath: '/ext',
      globalStorageUri: Uri.parse('file:///storage'),
      globalState: memento(),
      workspaceState: memento(),
    });

    // The key runs the command by its id, as the palette does: VS Code refuses a second
    // registration of an id, so there is one handler, and the toggle's own.
    await runCommand(binding?.command ?? '');

    expect(settingUpdates).toEqual([
      { name: 'cel-lens.inlineInfo', value: false, target: ConfigurationTarget.Global },
    ]);
    expect((await commands.getCommands()).filter((each) => each === binding?.command)).toHaveLength(
      1,
    );
  });

  it('sends the example results setting to the running server when it changes', async () => {
    settings.set('cel-lens.serverPath', process.execPath);
    changeActiveEditor('yaml');
    activate({
      subscriptions: [],
      extensionPath: '/ext',
      globalStorageUri: Uri.parse('file:///storage'),
      globalState: memento(),
      workspaceState: memento(),
    });
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
    activate({
      subscriptions: [],
      extensionPath: '/ext',
      globalStorageUri: Uri.parse('file:///storage'),
      globalState: memento(),
      workspaceState: memento(),
    });

    await runCommand(TOGGLE_EXAMPLE_RESULTS_COMMAND);

    expect(settingUpdates).toEqual([
      { name: 'cel-lens.exampleResults', value: false, target: ConfigurationTarget.Global },
    ]);
  });

  it('asks the engine for the results of the rule when the part of its line runs, and shows them in a picker', async () => {
    settings.set('cel-lens.serverPath', process.execPath);
    changeActiveEditor('yaml');
    activate({
      subscriptions: [],
      extensionPath: '/ext',
      globalStorageUri: Uri.parse('file:///storage'),
      globalState: memento(),
      workspaceState: memento(),
    });
    await vi.waitFor(() => {
      expect(statusBarItems[0]?.text).toBe('CEL · K8s 1.37');
    });
    const rule = {
      uri: 'file:///workspace/crd.yaml',
      range: { start: { line: 15, character: 30 }, end: { line: 15, character: 52 } },
    };

    await runCommand(SHOW_EXAMPLE_RESULTS_COMMAND, rule);

    expect(requestsButTheList()).toEqual([
      {
        method: 'workspace/executeCommand',
        params: { command: 'cel-lens.exampleResults', arguments: [rule] },
      },
    ]);
    expect(quickPicks.created).toMatchObject([{ title: 'Example results of the rule on line 16' }]);
  });

  it('makes the cel-lens view, and asks the engine for the list of the rules once it runs', async () => {
    settings.set('cel-lens.serverPath', process.execPath);
    addWorkspaceFolder('workspace');
    client.answers.set('cel-lens.ruleList', {
      documents: [
        {
          uri: 'file:///workspace/workspace/crd.yaml',
          folder: 'file:///workspace/workspace',
          file: 'crd.yaml',
          document: 'CustomResourceDefinition widgets.example.com',
          kind: 'CustomResourceDefinition',
          name: 'widgets.example.com',
          rules: [
            {
              id: 'file:///workspace/workspace/crd.yaml#r',
              label: 'at most 3',
              location: {
                uri: 'file:///workspace/workspace/crd.yaml',
                range: { start: { line: 4, character: 8 }, end: { line: 4, character: 20 } },
              },
              compile: { state: 'ok', problems: 0, messages: [] },
            },
          ],
        },
      ],
    });

    activate({
      subscriptions: [],
      extensionPath: '/ext',
      globalStorageUri: Uri.parse('file:///storage'),
      globalState: memento(),
      workspaceState: memento(),
    });

    expect(treeViews.map((view) => view.viewId)).toEqual(['cel-lens.rules']);
    await vi.waitFor(() => {
      expect(client.requests).toContainEqual({
        method: 'workspace/executeCommand',
        params: { command: 'cel-lens.ruleList', arguments: [] },
      });
    });
    const provider = treeViews[0]?.provider;
    await vi.waitFor(() => {
      expect(provider?.getChildren()).toMatchObject([{ label: 'widgets.example.com' }]);
    });
  });

  describe('the editor submenu', () => {
    const FILE = 'file:///workspace/workspace/crd.yaml';
    const RULE = {
      uri: FILE,
      range: { start: { line: 4, character: 8 }, end: { line: 4, character: 20 } },
    };

    /** Activates with an engine that lists one rule at lines 4 to 6 of the open file, the cursor in it. */
    async function withRuleOpen(): Promise<void> {
      settings.set('cel-lens.serverPath', process.execPath);
      addWorkspaceFolder('workspace');
      client.answers.set('cel-lens.ruleList', {
        documents: [
          {
            uri: FILE,
            folder: 'file:///workspace/workspace',
            file: 'crd.yaml',
            document: 'CustomResourceDefinition widgets.example.com',
            kind: 'CustomResourceDefinition',
            name: 'widgets.example.com',
            range: { start: { line: 0, character: 0 }, end: { line: 12, character: 0 } },
            rules: [
              {
                id: `${FILE}#r`,
                label: 'at most 3',
                location: RULE,
                span: { start: { line: 4, character: 0 }, end: { line: 6, character: 30 } },
                compile: { state: 'ok', problems: 0, messages: [] },
              },
            ],
          },
        ],
      });
      activeEditor.uri = FILE;
      changeActiveEditor('yaml');
      moveCursor(5, 3);
      activate({
        subscriptions: [],
        extensionPath: '/ext',
        globalStorageUri: Uri.parse('file:///storage'),
        globalState: memento(),
        workspaceState: memento(),
      });
      await vi.waitFor(() => {
        expect(contextKeys.get('cel-lens.ruleAtCursor')).toBe('rule');
      });
      executedCommands.length = 0;
    }

    it('sets cel-lens.editorHasRules and cel-lens.ruleAtCursor from the engine’s list once it answers', async () => {
      await withRuleOpen();

      expect(contextKeys.get('cel-lens.editorHasRules')).toBe(true);
    });

    it.each([
      ['the editor’s URI, from the submenu', Uri.parse(FILE)],
      ['nothing, from the palette', undefined],
    ])(
      'shows the problems of the rule at the cursor when Show Problems runs with %s',
      async (_name, argument) => {
        await withRuleOpen();

        await runCommand(SHOW_RULE_COMMAND, argument);

        expect(shownDocuments).toMatchObject([
          { uri: FILE, selection: { startLine: 4, startCharacter: 8 } },
        ]);
        expect(executedCommands.map((each) => each.command)).toContain('editor.action.showHover');
      },
    );

    it('acts on the location it is given when the line or the view runs it, as before', async () => {
      await withRuleOpen();
      const other = {
        uri: 'file:///workspace/other.yaml',
        range: { start: { line: 1, character: 2 }, end: { line: 1, character: 9 } },
      };

      await runCommand(SHOW_RULE_COMMAND, other);

      expect(shownDocuments).toMatchObject([{ uri: 'file:///workspace/other.yaml' }]);
    });

    it('asks the engine for the example results of the rule at the cursor when Show Example Results runs from the submenu', async () => {
      await withRuleOpen();

      await runCommand(SHOW_EXAMPLE_RESULTS_COMMAND, Uri.parse(FILE));

      expect(requestsButTheList()).toEqual([
        {
          method: 'workspace/executeCommand',
          params: { command: 'cel-lens.exampleResults', arguments: [RULE] },
        },
      ]);
    });

    it.each([SHOW_RULE_COMMAND, SHOW_EXAMPLE_RESULTS_COMMAND])(
      'says in the status bar for 3 seconds that the cursor is in no rule, and does nothing else, when %s runs from a menu between rules',
      async (command) => {
        await withRuleOpen();
        moveCursor(9, 0);

        await runCommand(command, Uri.parse(FILE));

        expect(statusBarMessages).toEqual([
          { text: 'cel-lens: the cursor is in no rule', hideAfterMs: 3000 },
        ]);
        expect(shownDocuments).toEqual([]);
        expect(executedCommands).toEqual([]);
        expect(requestsButTheList()).toEqual([]);
        expect(quickPicks.created).toEqual([]);
      },
    );

    it.each([SHOW_RULE_COMMAND, SHOW_EXAMPLE_RESULTS_COMMAND])(
      'runs %s from the title menu of another group of a split editor on that group’s rule, not the active editor’s (review of #48)',
      async (command) => {
        await withRuleOpen();
        // The active editor is another file; the group whose menu was opened shows the file with a rule.
        const group = new FakeEditor({
          languageId: 'yaml',
          getText: () => '',
          uri: Uri.parse(FILE),
        });
        group.selection = { active: { line: 5, character: 3 } };
        visibleEditors.push(group);
        activeEditor.uri = 'file:///workspace/workspace/other.yaml';
        changeActiveEditor('yaml');
        statusBarMessages.length = 0;

        await runCommand(command, Uri.parse(FILE));

        expect(statusBarMessages).toEqual([]);
        if (command === SHOW_RULE_COMMAND) {
          expect(shownDocuments).toMatchObject([{ uri: FILE }]);
        } else {
          expect(requestsButTheList()).toEqual([
            {
              method: 'workspace/executeCommand',
              params: { command: 'cel-lens.exampleResults', arguments: [RULE] },
            },
          ]);
        }
      },
    );

    it.each([SHOW_RULE_COMMAND, SHOW_EXAMPLE_RESULTS_COMMAND])(
      'says there is no rule at the cursor when %s gets the URI of a file no visible editor shows, though the active editor is in a rule (review of #48)',
      async (command) => {
        await withRuleOpen();

        await runCommand(command, Uri.parse('file:///workspace/workspace/closed.yaml'));

        expect(statusBarMessages).toEqual([
          { text: 'cel-lens: the cursor is in no rule', hideAfterMs: 3000 },
        ]);
        expect(shownDocuments).toEqual([]);
        expect(requestsButTheList()).toEqual([]);
      },
    );

    it('reveals the rule at the cursor in the view, with the focus, from Reveal in Rule View', async () => {
      await withRuleOpen();
      // The view follows the cursor by itself, without the focus; that is not what is asked here.
      const before = treeViews[0]?.revealed.length ?? 0;

      await runCommand('cel-lens.rules.reveal');

      expect(treeViews[0]?.revealed.slice(before)).toMatchObject([
        {
          element: { kind: 'rule', label: 'at most 3' },
          options: { select: true, focus: true, expand: true },
        },
      ]);
    });

    it.each([['cel-lens.rules.showProblemsOnly'], ['cel-lens.rules.showAll']])(
      'opens the view when %s runs from the palette and the view is not showing',
      async (command) => {
        await withRuleOpen();
        const view = treeViews[0];
        if (view !== undefined) {
          view.visible = false;
        }

        await runCommand(command);

        expect(executedCommands).toContainEqual({ command: 'cel-lens.rules.focus', args: [] });
      },
    );

    it('does not open the view when a filter command runs while it shows, as from its title bar', async () => {
      await withRuleOpen();

      await runCommand('cel-lens.rules.showProblemsOnly');

      expect(executedCommands).toEqual([]);
    });
  });

  it('hands the engine’s notification of a change in the list to the view, which asks for what changed', async () => {
    settings.set('cel-lens.serverPath', process.execPath);
    addWorkspaceFolder('workspace');
    client.answers.set('cel-lens.ruleList', { generation: 1, documents: [] });
    activate({
      subscriptions: [],
      extensionPath: '/ext',
      globalStorageUri: Uri.parse('file:///storage'),
      globalState: memento(),
      workspaceState: memento(),
    });
    await vi.waitFor(() => {
      expect(client.requests).toHaveLength(1);
    });

    client.handlers.get('cel-lens/ruleListChanged')?.({ generation: 2 });

    await vi.waitFor(() => {
      expect(client.requests.at(-1)).toEqual({
        method: 'workspace/executeCommand',
        params: { command: 'cel-lens.ruleList', arguments: [{ since: 1 }] },
      });
    });
  });

  it('switches the view to the rules with a problem with the title menu’s commands, and keeps the choice in the workspace’s state', async () => {
    const workspaceState = memento();
    activate({
      subscriptions: [],
      extensionPath: '/ext',
      globalStorageUri: Uri.parse('file:///storage'),
      globalState: memento(),
      workspaceState,
    });

    await runCommand('cel-lens.rules.showProblemsOnly');

    expect(contextKeys.get('cel-lens.ruleView.problemsOnly')).toBe(true);
    expect(workspaceState.get('cel-lens.ruleView.problemsOnly')).toBe(true);

    await runCommand('cel-lens.rules.showAll');

    expect(contextKeys.get('cel-lens.ruleView.problemsOnly')).toBe(false);
    expect(workspaceState.get('cel-lens.ruleView.problemsOnly')).toBe(false);
  });

  it('reads the filter kept for the workspace when it activates', () => {
    const workspaceState = memento();
    void workspaceState.update('cel-lens.ruleView.problemsOnly', true);

    activate({
      subscriptions: [],
      extensionPath: '/ext',
      globalStorageUri: Uri.parse('file:///storage'),
      globalState: memento(),
      workspaceState,
    });

    expect(contextKeys.get('cel-lens.ruleView.problemsOnly')).toBe(true);
  });

  it('turns following the editor off and on with the title menu’s commands, and applies the setting live', async () => {
    activate({
      subscriptions: [],
      extensionPath: '/ext',
      globalStorageUri: Uri.parse('file:///storage'),
      globalState: memento(),
      workspaceState: memento(),
    });
    expect(contextKeys.get('cel-lens.ruleView.followEditor')).toBe(true);

    await runCommand('cel-lens.rules.stopFollowing');
    settings.set('cel-lens.ruleView.followEditor', false);
    changeConfiguration('cel-lens.ruleView.followEditor');

    expect(settingUpdates).toContainEqual({
      name: 'cel-lens.ruleView.followEditor',
      value: false,
      target: ConfigurationTarget.Global,
    });
    expect(contextKeys.get('cel-lens.ruleView.followEditor')).toBe(false);

    await runCommand('cel-lens.rules.followEditor');
    settings.set('cel-lens.ruleView.followEditor', true);
    changeConfiguration('cel-lens.ruleView.followEditor');

    expect(contextKeys.get('cel-lens.ruleView.followEditor')).toBe(true);
  });

  it('runs Show Problems from the view’s context menu for the rule the menu was opened on', async () => {
    activate({
      subscriptions: [],
      extensionPath: '/ext',
      globalStorageUri: Uri.parse('file:///storage'),
      globalState: memento(),
      workspaceState: memento(),
    });
    const location = {
      uri: 'file:///workspace/crd.yaml',
      range: { start: { line: 4, character: 8 }, end: { line: 4, character: 20 } },
    };

    await runCommand('cel-lens.rules.showProblems', { kind: 'rule', location });

    expect(executedCommands).toContainEqual({ command: 'cel-lens.showRule', args: [location] });
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
    activate({
      subscriptions: [],
      extensionPath: '/ext',
      globalStorageUri: Uri.parse('file:///storage'),
      globalState: memento(),
      workspaceState: memento(),
    });
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
    activate({
      subscriptions: [],
      extensionPath: '/ext',
      globalStorageUri: Uri.parse('file:///storage'),
      globalState: memento(),
      workspaceState: memento(),
    });
    quickPicks.next = 'Classic';

    await runCommand(CHOOSE_COLOR_STYLE_COMMAND);

    expect(settingUpdates).toMatchObject([{ name: 'cel-lens.colorStyle', value: 'classic' }]);
  });

  it('paints on activation and removes the decorations when it is disposed', () => {
    const editor = new FakeEditor({ languageId: 'yaml', getText: () => CRD });
    visibleEditors.push(editor);
    const subscriptions: vscode.Disposable[] = [];
    activate({
      subscriptions,
      extensionPath: '/ext',
      globalStorageUri: Uri.parse('file:///storage'),
      globalState: memento(),
      workspaceState: memento(),
    });
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
    activate({
      subscriptions,
      extensionPath: '/ext',
      globalStorageUri: Uri.parse('file:///storage'),
      globalState: memento(),
      workspaceState: memento(),
    });

    for (const subscription of subscriptions) {
      subscription.dispose();
    }

    expect(await commands.getCommands()).toEqual([]);
  });

  it('registers the commands of the parts of the line once, and frees them on dispose for the next activation', async () => {
    const subscriptions: vscode.Disposable[] = [];
    activate({
      subscriptions,
      extensionPath: '/ext',
      globalStorageUri: Uri.parse('file:///storage'),
      globalState: memento(),
      workspaceState: memento(),
    });
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
      activate({
        subscriptions: [],
        extensionPath: '/ext',
        globalStorageUri: Uri.parse('file:///storage'),
        globalState: memento(),
        workspaceState: memento(),
      });
    }).not.toThrow();
  });

  it('shows the hover of a rule with no problem when its summary part runs', async () => {
    activate({
      subscriptions: [],
      extensionPath: '/ext',
      globalStorageUri: Uri.parse('file:///storage'),
      globalState: memento(),
      workspaceState: memento(),
    });
    const range = { start: { line: 1, character: 10 }, end: { line: 1, character: 20 } };

    await runCommand(SHOW_RULE_COMMAND, { uri: 'file:///workspace/crd.yaml', range });

    expect(shownDocuments).toMatchObject([{ uri: 'file:///workspace/crd.yaml' }]);
    expect(executedCommands).toEqual([
      { command: 'closeMarkersNavigation', args: [] },
      { command: 'editor.action.showHover', args: [] },
    ]);
  });

  it('lists the values a line leaves out when its +N more part runs, and runs the one picked', async () => {
    activate({
      subscriptions: [],
      extensionPath: '/ext',
      globalStorageUri: Uri.parse('file:///storage'),
      globalState: memento(),
      workspaceState: memento(),
    });
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
    activate({
      subscriptions: [],
      extensionPath: '/ext',
      globalStorageUri: Uri.parse('file:///storage'),
      globalState: memento(),
      workspaceState: memento(),
    });
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

    expect(requestsButTheList()).toEqual([
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
    activate({
      subscriptions: [],
      extensionPath: '/ext',
      globalStorageUri: Uri.parse('file:///storage'),
      globalState: memento(),
      workspaceState: memento(),
    });

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
    expect(guide).toContain('cel-lens-core` v0.33.0');
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
