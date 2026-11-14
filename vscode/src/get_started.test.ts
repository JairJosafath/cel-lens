import {
  chmod,
  chown,
  lstat,
  mkdir,
  readFile,
  readdir,
  rm,
  symlink,
  writeFile,
} from 'node:fs/promises';
import { platform, tmpdir, userInfo } from 'node:os';
import { basename, join } from 'node:path';

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import {
  type FakeDiagnostic,
  DiagnosticSeverity,
  FakeTextDocument,
  Range,
  TabInputText,
  Uri,
  changeDiagnostics,
  closeDocument,
  contextKeys,
  createdDirectories,
  errorChoice,
  executedCommands,
  fileTexts,
  openDocuments,
  openTabs,
  reset,
  shownDocuments,
  shownErrors,
  shownInfos,
  writeFailure,
  writtenFiles,
} from '../test/vscode';
import {
  GetStarted,
  SAMPLE_FIXED_CONTEXT,
  SAMPLE_PROBLEM_WAIT_MS,
  WALKTHROUGH_ID,
  openWalkthrough,
  tempFolder,
} from './get_started';
import type { ServerState } from './kubernetes_version';
import { SAMPLE_CRD, SAMPLE_RULES_LINE } from './sample_crd';

/** How a fresh copy is shown: in an editor that stays open, the cursor at its rules. */
const FRESH = {
  uri: 'file:///storage/get-started/widgets.yaml',
  selection: new Range(SAMPLE_RULES_LINE, 0, SAMPLE_RULES_LINE, 0),
  preview: false,
};

const STORAGE = Uri.parse('file:///storage');
const FOLDER = 'file:///storage/get-started';
const SAMPLE = 'file:///storage/get-started/widgets.yaml';

const RUNNING: ServerState = { kind: 'running', info: undefined };

function log(): { info: (message: string) => void; error: (message: string) => void } {
  return { info: vi.fn(), error: vi.fn() };
}

function error(line: number, character: number): FakeDiagnostic {
  return {
    range: { start: { line, character }, end: { line, character: character + 7 } },
    severity: DiagnosticSeverity.Error,
    source: 'cel-lens',
    message: 'undefined field: replica',
  };
}

function warning(line: number, character: number): FakeDiagnostic {
  return { ...error(line, character), severity: DiagnosticSeverity.Warning };
}

/** The sample open in an editor, as VS Code holds it: a tab and a document. */
function openSampleEditor(text = SAMPLE_CRD): FakeTextDocument {
  const document = new FakeTextDocument(Uri.parse(SAMPLE), 'yaml', text);
  openDocuments.push(document);
  openTabs.push({ input: new TabInputText(Uri.parse(SAMPLE)) });
  return document;
}

/** Closes the sample's editor: the tab goes, then the document. */
function closeSampleEditor(): void {
  openTabs.splice(
    openTabs.findIndex((tab) => tab.input.uri.toString() === SAMPLE),
    1,
  );
  closeDocument({ uri: Uri.parse(SAMPLE) });
}

let disposables: { dispose(): void }[] = [];

function getStarted(state: () => ServerState = () => RUNNING, storage = STORAGE): GetStarted {
  const made = new GetStarted(storage, state, log());
  disposables.push(made);
  return made;
}

afterEach(() => {
  for (const each of disposables) {
    each.dispose();
  }
  disposables = [];
  vi.useRealTimers();
  reset();
});

describe('openWalkthrough', () => {
  it('opens the walkthrough by the extension’s id and its own', async () => {
    await openWalkthrough();

    expect(executedCommands).toEqual([
      { command: 'workbench.action.openWalkthrough', args: [WALKTHROUGH_ID] },
    ]);
  });
});

describe('Open the Sample CRD', () => {
  it('writes the sample to get-started/widgets.yaml in the storage folder, making the folder, and opens it at its rules in an editor that stays open', async () => {
    const uri = await getStarted().openSample();

    expect(uri?.toString()).toBe(SAMPLE);
    expect(createdDirectories).toEqual([FOLDER]);
    expect(writtenFiles).toEqual([SAMPLE]);
    expect(fileTexts.get(SAMPLE)).toBe(SAMPLE_CRD);
    expect(shownDocuments).toEqual([FRESH]);
  });

  it('writes nothing while the sample is open and edited, and shows it', async () => {
    const document = openSampleEditor(SAMPLE_CRD.replace('self.replica >', 'self.replicas >'));
    document.isDirty = true;

    await getStarted().openSample();

    expect(writtenFiles).toEqual([]);
    expect(shownDocuments).toEqual([{ uri: SAMPLE, selection: undefined, preview: false }]);
  });

  it('writes a fresh copy once the sample was closed', async () => {
    const started = getStarted();
    await started.openSample();
    fileTexts.set(SAMPLE, SAMPLE_CRD.replace('self.replica >', 'self.replicas >'));
    closeSampleEditor();

    await started.openSample();

    expect(writtenFiles).toEqual([SAMPLE, SAMPLE]);
    expect(fileTexts.get(SAMPLE)).toBe(SAMPLE_CRD);
    expect(shownDocuments).toEqual([FRESH, FRESH]);
  });

  describe('when the storage is not on disk', () => {
    const remote = Uri.parse('vscode-remote://ssh-remote+box/home/me/.vscode-server/storage');

    afterEach(async () => {
      for (const name of await readdir(tmpdir())) {
        if (name === basename(tempFolder()) || name.startsWith(`${basename(tempFolder())}-`)) {
          await rm(join(tmpdir(), name), { recursive: true, force: true });
        }
      }
    });

    it.runIf(process.geteuid?.() === 0)(
      'writes the sample in a new private folder, and shows it, when another user made the named folder first',
      async () => {
        await mkdir(tempFolder(), { mode: 0o755 });
        await chown(tempFolder(), 65534, 65534);
        const started = getStarted(() => RUNNING, remote);

        const uri = await started.openSample();

        const folder = (await readdir(tmpdir())).find((name) =>
          name.startsWith(`${basename(tempFolder())}-`),
        );
        expect(folder).toBeDefined();
        const path = join(tmpdir(), folder ?? '', 'get-started', 'widgets.yaml');
        expect(uri?.toString()).toBe(Uri.file(path).toString());
        expect(await readFile(path, 'utf8')).toBe(SAMPLE_CRD);
        expect((await lstat(join(tmpdir(), folder ?? ''))).mode & 0o777).toBe(0o700);
        expect(shownDocuments.map((shown) => shown.uri)).toEqual([Uri.file(path).toString()]);
        expect(shownErrors).toEqual([]);
      },
    );

    it.runIf(platform() !== 'win32')(
      'writes nothing in a temporary folder every user may write in without the sticky bit, and says it cannot write safely, without Remove it',
      async () => {
        // The test's own TMPDIR, which src/tmpdir_guard.ts makes for the run; never the system's.
        expect(basename(tmpdir())).toMatch(/^cel-lens-leaks-/);
        const { mode } = await lstat(tmpdir());
        await chmod(tmpdir(), 0o777);
        try {
          expect(await getStarted(() => RUNNING, remote).openSample()).toBeUndefined();
        } finally {
          await chmod(tmpdir(), mode & 0o7777);
        }

        expect(shownErrors).toEqual([
          {
            message: `cel-lens cannot write the sample CRD safely, because ${tmpdir()} lets every user replace what is in it (it is writable by all and not sticky).`,
            buttons: [],
          },
        ]);
        expect(shownDocuments).toEqual([]);
      },
    );

    it('writes into a folder of the system’s temporary folder named for the user, for the user alone', async () => {
      const uri = await getStarted(() => RUNNING, remote).openSample();

      const path = join(tempFolder(), 'get-started', 'widgets.yaml');
      expect(tempFolder()).toBe(
        join(tmpdir(), `cel-lens-${String(process.geteuid?.() ?? userInfo().username)}`),
      );
      expect(uri?.toString()).toBe(Uri.file(path).toString());
      expect(uri?.scheme).toBe('file');
      expect(await readFile(path, 'utf8')).toBe(SAMPLE_CRD);
      if (platform() !== 'win32') {
        expect((await lstat(tempFolder())).mode & 0o777).toBe(0o700);
      }
      expect(writtenFiles).toEqual([]);
      expect(shownDocuments.map((shown) => shown.uri)).toEqual([Uri.file(path).toString()]);
    });

    it.runIf(platform() !== 'win32')(
      'refuses a sample planted as a symbolic link, says why once, and shows nothing',
      async () => {
        const victim = join(tmpdir(), 'cel-lens-victim.txt');
        await writeFile(victim, 'keep me');
        const path = join(tempFolder(), 'get-started', 'widgets.yaml');
        await mkdir(join(tempFolder(), 'get-started'), { recursive: true, mode: 0o700 });
        await symlink(victim, path);
        try {
          expect(await getStarted(() => RUNNING, remote).openSample()).toBeUndefined();

          expect(await readFile(victim, 'utf8')).toBe('keep me');
          expect(shownErrors).toEqual([
            {
              message: `cel-lens did not write the sample CRD, because ${path} is a symbolic link. Remove it, then open the sample again.`,
              buttons: [],
            },
          ]);
          expect(shownDocuments).toEqual([]);
        } finally {
          await rm(victim, { force: true });
        }
      },
    );
  });

  it('says once that the sample could not be written, with the logs a button away, and shows nothing', async () => {
    writeFailure.error = new Error('read-only file system');
    errorChoice.next = 'Show Logs';

    expect(await getStarted().openSample()).toBeUndefined();

    expect(shownErrors).toEqual([
      {
        message: `cel-lens could not open the sample CRD at ${SAMPLE}: read-only file system`,
        buttons: ['Show Logs'],
      },
    ]);
    expect(shownDocuments).toEqual([]);
    expect(executedCommands).toEqual([{ command: 'cel-lens.showLogs', args: [] }]);
  });
});

describe('Fix the Problem', () => {
  it('shows the sample with the cursor at the start of its first error and opens the quick fix list', async () => {
    changeDiagnostics(SAMPLE, [warning(40, 4), error(46, 25), error(44, 30)]);

    await getStarted().fixProblem();

    expect(writtenFiles).toEqual([SAMPLE]);
    expect(shownDocuments.at(-1)).toEqual({
      uri: SAMPLE,
      selection: new Range(44, 30, 44, 30),
      preview: false,
    });
    expect(executedCommands).toEqual([{ command: 'editor.action.quickFix', args: [] }]);
    expect(shownErrors).toEqual([]);
    expect(shownInfos).toEqual([]);
  });

  it('waits for the engine’s error on the sample, then opens the quick fix list there', async () => {
    vi.useFakeTimers();
    const fixing = getStarted().fixProblem();
    await vi.advanceTimersByTimeAsync(SAMPLE_PROBLEM_WAIT_MS - 1);
    expect(executedCommands).toEqual([]);

    changeDiagnostics('file:///workspace/other.yaml', [error(1, 1)]);
    changeDiagnostics(SAMPLE, [error(46, 25)]);
    await fixing;

    expect(shownDocuments.at(-1)?.selection).toEqual(new Range(46, 25, 46, 25));
    expect(executedCommands).toEqual([{ command: 'editor.action.quickFix', args: [] }]);
  });

  it('says once, after 10 s with no error while the engine runs, that the sample has no problem left and how to get a fresh one', async () => {
    vi.useFakeTimers();
    openSampleEditor(SAMPLE_CRD.replace('self.replica >', 'self.replicas >'));
    changeDiagnostics(SAMPLE, [warning(40, 4)]);

    const fixing = getStarted().fixProblem();
    await vi.advanceTimersByTimeAsync(SAMPLE_PROBLEM_WAIT_MS);
    await fixing;

    expect(shownInfos).toEqual([
      'The sample CRD has no problem left to fix. Close it, and Open the Sample CRD writes a fresh one.',
    ]);
    expect(shownErrors).toEqual([]);
    expect(executedCommands).toEqual([]);
  });

  it.each([['failed'], ['crashed'], ['stopped']] as const)(
    'says at once, with the engine %s, that the engine is not running, with Show Logs and Restart Engine',
    async (kind) => {
      errorChoice.next = 'Restart Engine';

      await getStarted(() => ({ kind })).fixProblem();

      expect(shownErrors).toEqual([
        {
          message: 'The cel-lens engine is not running, so it cannot check the sample CRD.',
          buttons: ['Show Logs', 'Restart Engine'],
        },
      ]);
      expect(executedCommands).toEqual([{ command: 'cel-lens.restartServer', args: [] }]);
      expect(shownInfos).toEqual([]);
    },
  );

  it('says the engine is not running when it is still starting after 10 s, and Show Logs opens the logs', async () => {
    vi.useFakeTimers();
    errorChoice.next = 'Show Logs';

    const fixing = getStarted(() => ({ kind: 'starting' })).fixProblem();
    await vi.advanceTimersByTimeAsync(SAMPLE_PROBLEM_WAIT_MS);
    await fixing;

    expect(shownErrors.map((shown) => shown.message)).toEqual([
      'The cel-lens engine is not running, so it cannot check the sample CRD.',
    ]);
    expect(executedCommands).toEqual([{ command: 'cel-lens.showLogs', args: [] }]);
  });

  it('runs once at a time: a second press while it waits says nothing more', async () => {
    vi.useFakeTimers();
    const started = getStarted();

    const first = started.fixProblem();
    await started.fixProblem();
    await vi.advanceTimersByTimeAsync(SAMPLE_PROBLEM_WAIT_MS);
    await first;

    expect(shownInfos).toHaveLength(1);
  });
});

describe('cel-lens.sampleFixed', () => {
  beforeEach(() => {
    contextKeys.clear();
  });

  it('becomes true once the open sample, having had an error, has none after an edit', async () => {
    getStarted();
    const document = openSampleEditor();
    changeDiagnostics(SAMPLE, [error(44, 30)]);
    expect(contextKeys.has(SAMPLE_FIXED_CONTEXT)).toBe(false);

    document.version += 1;
    changeDiagnostics(SAMPLE, [warning(40, 4)]);
    await Promise.resolve();

    expect(contextKeys.get(SAMPLE_FIXED_CONTEXT)).toBe(true);
  });

  it('counts the errors that come before the sample’s tab, as they do when it opens', async () => {
    getStarted();
    const document = new FakeTextDocument(Uri.parse(SAMPLE), 'yaml', SAMPLE_CRD);
    openDocuments.push(document);
    changeDiagnostics(SAMPLE, [error(30, 30)]);
    openTabs.push({ input: new TabInputText(Uri.parse(SAMPLE)) });

    document.version += 1;
    changeDiagnostics(SAMPLE, []);
    await Promise.resolve();

    expect(contextKeys.get(SAMPLE_FIXED_CONTEXT)).toBe(true);
  });

  it('stays unset when the errors go without an edit, as when the engine restarts', async () => {
    getStarted();
    openSampleEditor();
    changeDiagnostics(SAMPLE, [error(44, 30)]);

    changeDiagnostics(SAMPLE, []);
    await Promise.resolve();

    expect(contextKeys.has(SAMPLE_FIXED_CONTEXT)).toBe(false);
  });

  it('stays unset when the sample closes and its errors are cleared', async () => {
    getStarted();
    const document = openSampleEditor();
    changeDiagnostics(SAMPLE, [error(44, 30)]);
    document.version += 1;

    closeSampleEditor();
    changeDiagnostics(SAMPLE, []);
    await Promise.resolve();

    expect(contextKeys.has(SAMPLE_FIXED_CONTEXT)).toBe(false);
  });

  it('stays unset for a sample that never had an error', async () => {
    getStarted();
    const document = openSampleEditor();
    document.version += 1;

    changeDiagnostics(SAMPLE, []);
    await Promise.resolve();

    expect(contextKeys.has(SAMPLE_FIXED_CONTEXT)).toBe(false);
  });

  it('becomes false when Open the Sample CRD writes a fresh copy', async () => {
    const started = getStarted();
    const document = openSampleEditor();
    changeDiagnostics(SAMPLE, [error(44, 30)]);
    document.version += 1;
    changeDiagnostics(SAMPLE, []);
    await Promise.resolve();
    expect(contextKeys.get(SAMPLE_FIXED_CONTEXT)).toBe(true);
    closeSampleEditor();

    await started.openSample();

    expect(writtenFiles).toEqual([SAMPLE]);
    expect(contextKeys.get(SAMPLE_FIXED_CONTEXT)).toBe(false);
  });

  it('listens no more once disposed', async () => {
    const started = getStarted();
    const document = openSampleEditor();
    changeDiagnostics(SAMPLE, [error(44, 30)]);
    started.dispose();

    document.version += 1;
    changeDiagnostics(SAMPLE, []);
    await Promise.resolve();

    expect(contextKeys.has(SAMPLE_FIXED_CONTEXT)).toBe(false);
  });
});
