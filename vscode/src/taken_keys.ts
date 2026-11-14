// The keyboard shortcuts that VS Code and the common extensions take, so a test can fail on a
// shortcut this extension contributes that clashes with one of them. `npm run keys` fetches the
// default keybindings of VS Code for Linux, Windows and macOS (from
// github.com/codebling/vs-code-default-keybindings) and the `package.json` of each extension the
// plan of cel-lens#3 names, from its repository, and writes `src/taken_keys.json`: per source its
// name and version, and its keys normalised, for Windows and Linux (`pc`) and for macOS (`mac`).
// It needs the network, so `make check` does not run it; `src/keybindings.test.ts` reads the file.
// The pure functions below are tested in `src/taken_keys.test.ts`.
import { writeFile } from 'node:fs/promises';
import { argv, exit, stderr, stdout } from 'node:process';
import { fileURLToPath } from 'node:url';

import { isRecord } from './is_record.ts';

/** One shortcut a source takes: its key as chords, normalised, the command it runs and where it applies. */
export interface TakenKey {
  /** The chords of the key in order: `ctrl+k l` is `['ctrl+k', 'l']`. Lower case, modifiers in the order ctrl, shift, alt, meta. */
  readonly chords: readonly string[];
  readonly command: string;
  /** The `when` clause, absent when the binding applies everywhere. */
  readonly when?: string;
}

/** A place that takes shortcuts: VS Code on one platform, or an extension. */
export interface TakenSource {
  readonly name: string;
  readonly version: string;
  /** Where it was read from, a URL. */
  readonly origin: string;
  /** The keys that apply on Windows and Linux. */
  readonly pc: readonly TakenKey[];
  /** The keys that apply on macOS. */
  readonly mac: readonly TakenKey[];
}

/** What `src/taken_keys.json` holds. */
export interface TakenKeys {
  readonly sources: readonly TakenSource[];
}

const MODIFIER_ORDER = ['ctrl', 'shift', 'alt', 'meta'] as const;
const MODIFIER_NAMES: Readonly<Record<string, (typeof MODIFIER_ORDER)[number]>> = {
  ctrl: 'ctrl',
  control: 'ctrl',
  shift: 'shift',
  alt: 'alt',
  option: 'alt',
  cmd: 'meta',
  command: 'meta',
  meta: 'meta',
  win: 'meta',
  windows: 'meta',
  super: 'meta',
};

/** One chord, such as `Shift+Ctrl+R`, as `ctrl+shift+r`: lower case, the modifiers in one order. */
function normalizeChord(chord: string): string {
  const lower = chord.toLowerCase();
  // The key itself may be `+`, as in `ctrl++`; it is what follows the last separator that is not the key.
  const keyAt = lower.endsWith('+') ? lower.length - 1 : lower.lastIndexOf('+') + 1;
  const key = lower.slice(keyAt);
  const modifiers = new Set<string>();
  for (const word of lower.slice(0, keyAt).split('+')) {
    const modifier = MODIFIER_NAMES[word];
    if (modifier !== undefined) {
      modifiers.add(modifier);
    }
  }
  return [...MODIFIER_ORDER.filter((each) => modifiers.has(each)), key].join('+');
}

/** A key as the manifest writes it, `ctrl+k l`, as its chords, normalised: `['ctrl+k', 'l']`. */
export function normalizeKey(key: string): string[] {
  return key
    .trim()
    .split(/\s+/)
    .filter((chord) => chord !== '')
    .map(normalizeChord);
}

function text(value: unknown): string | undefined {
  return typeof value === 'string' && value !== '' ? value : undefined;
}

function entry(key: string, command: unknown, when: unknown): TakenKey | undefined {
  const name = text(command);
  if (name === undefined || name.startsWith('-')) {
    // A command with a leading `-` removes a binding; it takes no key.
    return undefined;
  }
  const clause = text(when);
  return {
    chords: normalizeKey(key),
    command: name,
    ...(clause !== undefined && { when: clause }),
  };
}

/** VS Code's default keybindings file (JSON with a comment line first): its version, and its keys. */
export function parseDefaultKeybindings(file: string): { version: string; keys: TakenKey[] } {
  const version = /Visual Studio Code (\d+(?:\.\d+)*)/.exec(file)?.[1];
  if (version === undefined) {
    throw new Error('The default keybindings file does not say which VS Code it is for.');
  }
  const parsed: unknown = JSON.parse(file.replace(/^\s*\/\/.*$/gm, ''));
  if (!Array.isArray(parsed)) {
    throw new Error('The default keybindings file is not a list.');
  }
  const keys = parsed.flatMap((item: unknown) => {
    const key = isRecord(item) ? text(item['key']) : undefined;
    const taken =
      isRecord(item) && key !== undefined ? entry(key, item['command'], item['when']) : undefined;
    return taken === undefined ? [] : [taken];
  });
  return { version, keys };
}

/**
 * An extension's `package.json`: its version, and the keys its `contributes.keybindings` take. On
 * Windows and Linux they are `key`, `win` and `linux`; on macOS `mac`, else `key`.
 */
export function extensionKeys(manifest: unknown): {
  version: string;
  pc: TakenKey[];
  mac: TakenKey[];
} {
  if (!isRecord(manifest)) {
    throw new Error('The package.json is not an object.');
  }
  const version = text(manifest['version']);
  if (version === undefined) {
    throw new Error('The package.json has no version.');
  }
  const contributes = manifest['contributes'];
  const list = isRecord(contributes) ? contributes['keybindings'] : undefined;
  const pc: TakenKey[] = [];
  const mac: TakenKey[] = [];
  for (const item of Array.isArray(list) ? (list as unknown[]) : []) {
    if (!isRecord(item)) {
      continue;
    }
    for (const key of [item['key'], item['win'], item['linux']]) {
      const taken =
        text(key) === undefined ? undefined : entry(text(key) ?? '', item['command'], item['when']);
      if (taken !== undefined) {
        pc.push(taken);
      }
    }
    const macKey = text(item['mac']) ?? text(item['key']);
    const taken = macKey === undefined ? undefined : entry(macKey, item['command'], item['when']);
    if (taken !== undefined) {
      mac.push(taken);
    }
  }
  return { version, pc, mac };
}

/** Where each source is read from. */
const VS_CODE_FILES =
  'https://raw.githubusercontent.com/codebling/vs-code-default-keybindings/master';
const VS_CODE_PLATFORMS = [
  { name: 'VS Code (Linux)', file: 'linux.keybindings.json', platform: 'pc' },
  { name: 'VS Code (Windows)', file: 'windows.keybindings.json', platform: 'pc' },
  { name: 'VS Code (macOS)', file: 'macos.keybindings.json', platform: 'mac' },
] as const;

/** The extensions of the plan of cel-lens#3, each with the repository and the path of its `package.json`. */
export const EXTENSIONS = [
  { name: 'GitLens', repository: 'gitkraken/vscode-gitlens', path: 'package.json' },
  { name: 'Red Hat YAML', repository: 'redhat-developer/vscode-yaml', path: 'package.json' },
  {
    name: 'Kubernetes Tools',
    repository: 'vscode-kubernetes-tools/vscode-kubernetes-tools',
    path: 'package.json',
  },
  { name: 'Prettier', repository: 'prettier/prettier-vscode', path: 'package.json' },
  { name: 'ESLint', repository: 'microsoft/vscode-eslint', path: 'package.json' },
  {
    name: 'GitHub Copilot Chat',
    repository: 'microsoft/vscode-copilot-chat',
    path: 'package.json',
  },
  { name: 'Vim', repository: 'VSCodeVim/Vim', path: 'package.json' },
  { name: 'Docker', repository: 'microsoft/vscode-docker', path: 'package.json' },
  { name: 'Python', repository: 'microsoft/vscode-python', path: 'package.json' },
  { name: 'Jupyter', repository: 'microsoft/vscode-jupyter', path: 'package.json' },
  { name: 'Go', repository: 'golang/vscode-go', path: 'extension/package.json' },
  { name: 'Error Lens', repository: 'usernamehw/vscode-error-lens', path: 'package.json' },
  { name: 'Git Graph', repository: 'mhutchie/vscode-git-graph', path: 'package.json' },
  { name: 'Todo Tree', repository: 'Gruntfuggly/todo-tree', path: 'package.json' },
  { name: 'Helm Intellisense', repository: 'tim-koehler/Helm-Intellisense', path: 'package.json' },
] as const;

/** Fetches a URL's text; the script gives `fetch`, the tests a stand-in. */
export type TextFetcher = (url: string) => Promise<string>;

/** Reads every source with `fetchText`, in a fixed order, so a run with the same sources writes the same file. */
export async function collectTakenKeys(fetchText: TextFetcher): Promise<TakenKeys> {
  const sources: TakenSource[] = [];
  for (const { name, file, platform } of VS_CODE_PLATFORMS) {
    const origin = `${VS_CODE_FILES}/${file}`;
    const { version, keys } = parseDefaultKeybindings(await fetchText(origin));
    sources.push({
      name,
      version,
      origin,
      pc: platform === 'pc' ? keys : [],
      mac: platform === 'mac' ? keys : [],
    });
  }
  for (const { name, repository, path } of EXTENSIONS) {
    const origin = `https://raw.githubusercontent.com/${repository}/HEAD/${path}`;
    const { version, pc, mac } = extensionKeys(JSON.parse(await fetchText(origin)));
    sources.push({ name, version, origin, pc, mac });
  }
  return { sources };
}

const OUTPUT = fileURLToPath(new URL('taken_keys.json', import.meta.url));
const TIMEOUT_MS = 60_000;

async function fetchText(url: string): Promise<string> {
  const response = await fetch(url, { signal: AbortSignal.timeout(TIMEOUT_MS) });
  if (!response.ok) {
    throw new Error(`keys: ${url} answered ${String(response.status)}`);
  }
  return response.text();
}

async function main(): Promise<void> {
  const taken = await collectTakenKeys(fetchText);
  // Formatted as `prettier --check .` wants it, so the file is the same after `npm run format`.
  const { format } = await import('prettier');
  await writeFile(OUTPUT, await format(JSON.stringify(taken), { parser: 'json' }));
  for (const source of taken.sources) {
    stdout.write(
      `${source.name} ${source.version}: ${String(source.pc.length)} keys on Windows and Linux, ${String(source.mac.length)} on macOS\n`,
    );
  }
  stdout.write(`keys: wrote ${OUTPUT}\n`);
}

if (argv[1] === fileURLToPath(import.meta.url)) {
  main().catch((error: unknown) => {
    stderr.write(`${error instanceof Error ? error.message : String(error)}\n`);
    exit(1);
  });
}
