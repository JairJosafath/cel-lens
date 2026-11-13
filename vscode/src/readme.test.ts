import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import {
  readmeDefaultKubernetesVersion,
  readmeHeadings,
  readmeImages,
  readmeKubernetesVersions,
  readmePicturePlaceholders,
  readmeSection,
  readmeSettingDefaults,
} from './readme';

const README = readFileSync('README.md', 'utf8');
const IMAGE_BASE = 'https://raw.githubusercontent.com/JairJosafath/cel-lens/main/vscode/images/';
const PICTURES = [
  'inline-info.png',
  'give-type.gif',
  'colors.gif',
  'completion.gif',
  'problems.png',
  'hover.png',
  'completion.png',
  'quick-fix.png',
  'colors.png',
];

describe('readmeDefaultKubernetesVersion', () => {
  it.each([
    ['The default is `1.37`. See below.', '1.37'],
    ['Lists `1.36` and `1.37`.', undefined],
    ['', undefined],
  ])('reads %j as %j', (text, expected) => {
    expect(readmeDefaultKubernetesVersion(text)).toBe(expected);
  });
});

describe('the helpers', () => {
  const text = '# T\n\n## A\none `1.3` and `1.4`\n\n## B\n![x](https://e/x.png)\n';

  it.each([
    ['A', 'one `1.3` and `1.4`\n'],
    ['B', '![x](https://e/x.png)\n'],
    ['C', undefined],
  ])('readmeSection(%s) gives the lines under the heading', (heading, expected) => {
    expect(readmeSection(text, heading)?.trimStart()).toBe(expected);
  });

  it.each([
    ['one placeholder', '<!-- Picture to take: images/a.png, the line. -->\n', ['images/a.png']],
    [
      'two placeholders',
      '<!-- Picture to take: images/a.png -->\n<!-- Picture to take: images/b.gif, b -->\n',
      ['images/a.png', 'images/b.gif'],
    ],
    ['another comment', '<!-- a note -->\n', []],
    ['an image', '![x](https://e/x.png)\n', []],
  ])('readmePicturePlaceholders reads %s', (_name, text, expected) => {
    expect(readmePicturePlaceholders(text)).toEqual(expected);
  });

  it('lists headings, images and versions', () => {
    expect(readmeHeadings(text)).toEqual(['A', 'B']);
    expect(readmeImages(text)).toEqual([{ alt: 'x', url: 'https://e/x.png' }]);
    expect(readmeKubernetesVersions('`1.3`, `1.4`, `1.3`')).toEqual(['1.3', '1.4']);
  });

  it('reads the Default cell of each settings row', () => {
    const table =
      '| Setting | Default | Note |\n| --- | --- | --- |\n| `a.b` | `x` | n |\n| `a.c` | `""` | n |\n';
    expect([...readmeSettingDefaults(table)]).toEqual([
      ['a.b', 'x'],
      ['a.c', '""'],
    ]);
  });
});

describe('README.md as the store page', () => {
  it('says in one sentence what the extension does, right under the title', () => {
    const [title, blank, sentence] = README.split('\n');
    expect(title).toBe('# cel-lens for VS Code');
    expect(blank).toBe('');
    expect(sentence).toMatch(/^[^.]+\.$/);
  });

  it('has the store sections, in order', () => {
    expect(readmeHeadings(README)).toEqual([
      'What it looks like',
      'Install',
      'Give a value a type',
      'Example results',
      'Commands',
      'Settings',
      'Supported Kubernetes versions',
      'Experimental features',
      'Not supported yet',
      'License',
      'Contributing',
    ]);
  });

  it('names no store, no factory and no badge', () => {
    expect(README).not.toMatch(/marketplace|open vsx|factory|shields\.io|badge/i);
  });

  it('installs only from the .vsix of the GitHub releases', () => {
    const section = readmeSection(README, 'Install') ?? '';
    expect(section).toContain('https://github.com/JairJosafath/cel-lens/releases');
    expect(section).toContain('vscode-vX.Y.Z');
    expect(section).toContain('Install from VSIX...');
    expect(section).toContain('code --install-extension');
  });

  it('says experimental features are off until named in cel-lens.experimental', () => {
    const section = readmeSection(README, 'Experimental features') ?? '';
    expect(section).toContain('off until');
    expect(section).toContain('`cel-lens.experimental`');
  });

  it.each(['in the background', '20000', '1 MiB', 'checked again'])(
    'says in the crd-typed-policies row why a type stays dyn: %s',
    (phrase) => {
      const section = readmeSection(README, 'Experimental features') ?? '';
      const row = section.split('\n').find((line) => line.startsWith('| `crd-typed-policies`'));
      expect(row).toContain(phrase);
    },
  );

  it.each([
    'a single skipped file is logged on its own line',
    'many are reported in one summary log line with their count, the limit and the first three names',
  ])('says in the crd-typed-policies row how skipped files are logged: %s', (phrase) => {
    const section = readmeSection(README, 'Experimental features') ?? '';
    const row = section.split('\n').find((line) => line.startsWith('| `crd-typed-policies`'));
    expect(row).toContain(phrase);
    expect(row).not.toContain('logs each skipped file');
  });

  it('links the picture of the line above the rules and the GIF of Give it a type first, then the two GIFs and the five pictures, each with alt text and one line, as https URLs in the public repository', () => {
    const images = readmeImages(README);
    expect(images.map((image) => image.url)).toEqual(PICTURES.map((name) => IMAGE_BASE + name));
    expect(images.every((image) => image.alt.length > 0)).toBe(true);
    const section = readmeSection(README, 'What it looks like') ?? '';
    const lines = section
      .split('\n')
      .filter((line) => line.trim() !== '' && !line.startsWith('![') && !line.startsWith('<!--'));
    expect(lines).toHaveLength(PICTURES.length + readmePicturePlaceholders(section).length);
  });

  it('shows the line above the rules of a policy with a dyn value first, with its line', () => {
    const section = readmeSection(README, 'What it looks like') ?? '';
    const [picture, , line] = section.trimStart().split('\n');
    expect(picture).toMatch(
      /^!\[The line above each CEL rule of a ValidatingAdmissionPolicy: .*params as dyn with Give it a type\]\(.+\/inline-info\.png\)$/,
    );
    expect(line).toContain('cel-lens: Toggle Inline Info');
    expect(line).toContain(
      'a part whose type comes from `.cellens`, such as `variables.limit: int`, opens that entry beside the rule',
    );
  });

  it('shows Give it a type from an example of the workspace next, with its line', () => {
    const lines = (readmeSection(README, 'What it looks like') ?? '').trimStart().split('\n');
    const [picture, , line] = lines.slice(4);
    expect(picture).toMatch(
      /^!\[Give it a type: .*an example of the workspace picked.*the line showing the type it gave params\]\(.+\/give-type\.gif\)$/,
    );
    expect(line).toContain('**Give it a type**');
  });

  it('names no picture to take in the first section, and none of the line above a rule or the picker anywhere', () => {
    expect(readmePicturePlaceholders(readmeSection(README, 'What it looks like') ?? '')).toEqual(
      [],
    );
    expect(readmePicturePlaceholders(README)).not.toContain('images/inline-info.png');
    expect(readmePicturePlaceholders(README)).not.toContain('images/give-type.png');
  });

  it.each([
    '**Give it a type**',
    'opens `.cellens` beside the rule',
    'in the editor group that already shows it',
    "the engine's completion, hover and problems as you type",
    '`{name: string, replicas: int}`',
    '`go: ./api/v1#QuotaSpec`',
    '`kind: apps/v1 Deployment`',
    'offers `int` and `string` only',
    'A schema, a short type, a Go type or a kind would not fit it',
    'of the workspace folder of the `.cellens`',
    'its **Back** button returns to the first',
    'is saved when the `.cellens` the line names had no unsaved edits and nothing but that write changed it; when the engine writes another `.cellens`, nothing is saved',
    '**Edit** opens the same picker on what the value has',
    'open `.cellens` at its entry',
    '**Remove**',
    '**Undo** in the `.cellens` editor',
    '**cel-lens: Give a Value a Type…**',
  ])('says how to give a value a type: %s', (text) => {
    expect(readmeSection(README, 'Give a value a type')).toContain(text);
  });

  it('says how to give a type in each form, in the order of the picker, each under its heading with a picture for a person to take', () => {
    const section = readmeSection(README, 'Give a value a type') ?? '';
    const forms = section.split(/^(?=### )/m).slice(1);

    expect(forms.map((form) => form.split('\n')[0])).toEqual([
      '### A schema',
      '### A Go type',
      '### A short type',
      '### An example',
      '### A kind',
      '### A value of a CRD',
      '### Edit or remove a type',
    ]);
    expect(readmePicturePlaceholders(section)).toEqual([
      'images/give-type-schema.png',
      'images/give-type-go.png',
      'images/give-type-short.png',
      'images/give-type-example.png',
      'images/give-type-paste.png',
      'images/give-type-kind.png',
    ]);
    expect(
      forms.slice(0, 5).map((form) => readmePicturePlaceholders(form.split('\n')[2] ?? '')),
    ).toEqual([
      ['images/give-type-schema.png'],
      ['images/give-type-go.png'],
      ['images/give-type-short.png'],
      ['images/give-type-example.png'],
      ['images/give-type-kind.png'],
    ]);
    expect(forms.slice(0, 4).map((form) => /\*\*([^*]+)\*\*/.exec(form)?.[1])).toEqual([
      'Write a schema in .cellens…',
      'Use a Go type…',
      'Write a short type in .cellens…',
      'Examples in the workspace',
    ]);
  });

  it.each([
    'Pick one, and cel-lens works out an OpenAPI schema from it',
    "writes it as the entry's `schema:` and opens `.cellens` beside the rule at the entry",
    'shows once as an information problem at its key in `.cellens`',
    '**Paste an example…**',
    '**Use this example as the type** above it, or the check in its editor title',
    'and closing it yourself writes nothing',
    'the reason shows once and the editor stays open',
    'an example over 1 MiB, the most the engine reads, is refused at once',
    'or a whole resource of its kind that holds it',
    'a resource of another kind, such as an object reference, is the value itself',
    "which the engine tells from the value's kinds and says in a note",
    "a paste with `apiVersion` and `kind` at its root is read as a resource, and as the value itself when it has no value at the value's path",
    'named after the value, such as **Example of params.yaml**',
    'closing its editor never asks to save',
    'After a window reload, the examples of the window before are closed',
    'with its workspace folder in a multi-root workspace',
  ])('says how to give a type from an example: %s', (text) => {
    const example = (readmeSection(README, 'Give a value a type') ?? '').split('### An example')[1];
    expect(example?.split('### A kind')[0]).toContain(text);
  });

  it('tells nobody to set cel-lens.serverPath, or that an engine is too old, to give a value a type', () => {
    const section = readmeSection(README, 'Give a value a type') ?? '';
    expect(section).not.toContain('serverPath');
    expect(section).not.toContain('too old');
    expect(README).not.toMatch(/too old/);
  });

  it('says where the palette shows the commands, as their when clauses in package.json do', () => {
    const manifest = JSON.parse(readFileSync('package.json', 'utf8')) as {
      contributes: {
        commands: { command: string; title: string; category: string }[];
        menus: { commandPalette: { command: string; when: string }[] };
      };
    };
    const anyFile = manifest.contributes.menus.commandPalette
      .filter((entry) => entry.when === 'true')
      .map((entry) => manifest.contributes.commands.find((c) => c.command === entry.command));
    const intro = (readmeSection(README, 'Commands') ?? '').trim().split('\n')[0] ?? '';

    expect(anyFile.length).toBeGreaterThan(0);
    for (const command of anyFile) {
      expect(intro).toContain(
        `**${command?.category ?? ''}: ${command?.title ?? ''}** on any file`,
      );
    }
    expect(intro).toContain('the others on YAML files');
    expect(intro).not.toMatch(/Every command .* on YAML files/);
  });

  it('lists every command of the Command Palette in the Commands table, by its palette name', () => {
    const manifest = JSON.parse(readFileSync('package.json', 'utf8')) as {
      contributes: {
        commands: { command: string; title: string; category: string }[];
        menus: { commandPalette: { command: string; when: string }[] };
      };
    };
    const hidden = manifest.contributes.menus.commandPalette
      .filter((entry) => entry.when === 'false')
      .map((entry) => entry.command);
    const rows = (readmeSection(README, 'Commands') ?? '')
      .split('\n')
      .filter((row) => row.startsWith('| **'));
    const shown = manifest.contributes.commands.filter(({ command }) => !hidden.includes(command));

    expect(shown.length).toBeGreaterThan(0);
    expect(rows).toHaveLength(shown.length);
    for (const { category, title } of shown) {
      expect(rows.some((row) => row.startsWith(`| **${category}: ${title}**`))).toBe(true);
    }
  });

  it('only links images over https from the public repository', () => {
    for (const image of readmeImages(README)) {
      expect(image.url.startsWith(IMAGE_BASE)).toBe(true);
    }
  });

  it('names every setting of package.json in the Settings section', () => {
    const manifest = JSON.parse(readFileSync('package.json', 'utf8')) as {
      contributes: { configuration: { properties: Record<string, unknown> } };
    };
    const section = readmeSection(README, 'Settings') ?? '';
    const names = Object.keys(manifest.contributes.configuration.properties);
    expect(names.length).toBeGreaterThan(0);
    for (const name of names) {
      expect(section).toContain(`\`${name}\``);
    }
  });

  it('states each setting default as package.json has it', () => {
    const manifest = JSON.parse(readFileSync('package.json', 'utf8')) as {
      contributes: { configuration: { properties: Record<string, { default?: unknown }> } };
    };
    const written = readmeSettingDefaults(readmeSection(README, 'Settings') ?? '');
    const properties = Object.entries(manifest.contributes.configuration.properties);
    expect(properties.length).toBeGreaterThan(0);
    for (const [name, { default: value }] of properties) {
      const expected =
        value === '' ? '""' : typeof value === 'string' ? value : JSON.stringify(value);
      expect(written.get(name), name).toBe(expected);
    }
  });

  it('names the commands for the version and the color style instead of linking to CONTRIBUTING.md', () => {
    const versions = readmeSection(README, 'Supported Kubernetes versions') ?? '';
    expect(versions).toContain('status bar');
    expect(versions).toContain('Pick Kubernetes Version');
    const colorRow =
      README.split('\n').find((line) => line.includes('`cel-lens.colorStyle`')) ?? '';
    expect(colorRow).toContain('Choose Color Style');
    expect(versions).not.toContain('CONTRIBUTING.md');
    expect(colorRow).not.toContain('CONTRIBUTING.md');
  });

  it('says in the Example results section where the examples come from, what one expects, updates, the line, the hover, the warnings and the toggle, in that order', () => {
    const section = readmeSection(README, 'Example results') ?? '';
    const parts = section.split(/^(?=### )/m).slice(1);

    expect(parts.map((part) => part.split('\n')[0])).toEqual([
      '### Where the examples come from',
      '### What an example expects',
      '### Updates',
      '### The line above the rule',
      '### The hover and the warnings',
      '### Turn it off',
    ]);
  });

  it.each([
    'a version the CRD serves',
    '`config/samples/`',
    'a folder of it below its workspace folder, or a word of its file name',
    '`invalid`, `bad`, `refused`, `rejected` or `denied`',
    '`# cel-lens: refused`',
    '`# cel-lens: refused "at most 10"`',
    '`# cel-lens: allowed`',
    'which wins over the path',
    'The same object twice in one file is an update',
    '`3 examples pass`',
    '`refuses 1 example it should allow · 2 pass`',
    'the wrong verdicts come first',
    "the API server's message",
    'opens at its line beside the rule',
    'A wrong verdict is also a warning',
    '**cel-lens: Toggle Example Results**',
    '**Example results: on** or **off**',
    'without a reload',
    'offers a button to turn it on',
  ])('describes example results: %s', (text) => {
    expect(readmeSection(README, 'Example results')).toContain(text);
  });

  it('names the pictures of example results for the release step to take, each by its file', () => {
    expect(readmePicturePlaceholders(readmeSection(README, 'Example results') ?? '')).toEqual([
      'images/example-results-line.png',
      'images/example-results-picker.png',
    ]);
  });

  it('keeps dry runs under Not supported yet only for admission policies', () => {
    const dryRuns =
      (readmeSection(README, 'Not supported yet') ?? '')
        .split('\n')
        .find((line) => line.includes('Dry runs')) ?? '';

    expect(dryRuns).toContain('admission policies');
    expect(dryRuns).toContain('only the rules of a CRD run on examples so far');
  });

  it('lists Toggle Example Results in the Commands table and the setting with its toggle and status bar entry', () => {
    const row =
      README.split('\n').find((line) => line.startsWith('| `cel-lens.exampleResults`')) ?? '';

    expect(readmeSection(README, 'Commands')).toContain('| **cel-lens: Toggle Example Results**');
    expect(row).toContain('**cel-lens: Toggle Example Results**');
    expect(row).toContain('**Example results: on** or **off**');
    expect(row).toContain('Resource scope');
  });

  it('lists what is not supported yet', () => {
    const section = readmeSection(README, 'Not supported yet') ?? '';
    expect(section).toMatch(/Dry runs/);
    expect(section).toMatch(/Helm/);
    expect(section).toMatch(/[Kk]ubebuilder/);
  });
});
