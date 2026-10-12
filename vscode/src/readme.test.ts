import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import {
  readmeHeadings,
  readmeImages,
  readmeKubernetesVersions,
  readmeSection,
  readmeSettingDefaults,
} from './readme';

const README = readFileSync('README.md', 'utf8');
const IMAGE_BASE = 'https://raw.githubusercontent.com/JairJosafath/cel-lens/main/vscode/images/';
const PICTURES = ['problems.png', 'hover.png', 'completion.png', 'quick-fix.png', 'colors.png'];

describe('the helpers', () => {
  const text = '# T\n\n## A\none `1.3` and `1.4`\n\n## B\n![x](https://e/x.png)\n';

  it.each([
    ['A', 'one `1.3` and `1.4`\n'],
    ['B', '![x](https://e/x.png)\n'],
    ['C', undefined],
  ])('readmeSection(%s) gives the lines under the heading', (heading, expected) => {
    expect(readmeSection(text, heading)?.trimStart()).toBe(expected);
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

  it('has the store sections first, in order', () => {
    expect(readmeHeadings(README).slice(0, 5)).toEqual([
      'What it looks like',
      'Install',
      'Settings',
      'Supported Kubernetes versions',
      'Not supported yet',
    ]);
  });

  it('links the five pictures, with alt text, as https URLs in the public repository', () => {
    const images = readmeImages(README);
    expect(images.map((image) => image.url)).toEqual(PICTURES.map((name) => IMAGE_BASE + name));
    expect(images.every((image) => image.alt.length > 0)).toBe(true);
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

  it('lists what is not supported yet', () => {
    const section = readmeSection(README, 'Not supported yet') ?? '';
    expect(section).toMatch(/Dry runs/);
    expect(section).toMatch(/Helm/);
    expect(section).toMatch(/[Kk]ubebuilder/);
  });
});
