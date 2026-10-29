import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import {
  ColorThemeKind,
  ConfigurationTarget,
  FakeEditor,
  Uri,
  activeEditor,
  addWorkspaceFolder,
  changeActiveEditor,
  workspaceSettings,
  changeConfiguration,
  changeDocument,
  changeTheme,
  changeVisibleEditors,
  decorationTypes,
  openDocument,
  quickPicks,
  reset,
  scrollEditor,
  settingUpdates,
  settings,
  visibleEditors,
} from '../test/vscode';
import { celColors } from './cel_styles';
import {
  ColorPainter,
  PAINT_MARGIN_LINES,
  REPAINT_DELAY_MS,
  chooseColorStyle,
} from './color_painter';

const CRD = [
  'x-kubernetes-validations:',
  '  - rule: "self.a < 3"',
  '    message: "self.a must be small"',
  '  - rule: "self.b"',
  '',
].join('\n');

function yamlEditor(text = CRD): FakeEditor {
  const editor = new FakeEditor({ languageId: 'yaml', getText: () => text });
  visibleEditors.push(editor);
  return editor;
}

function colorsOf(editor: FakeEditor): unknown[] {
  return [...editor.decorations]
    .filter(([type, ranges]) => !type.disposed && ranges.length > 0)
    .map(([type]) => type.color);
}

describe('ColorPainter', () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
    reset();
  });

  it('paints the CEL of a visible YAML editor in the default style', () => {
    const editor = yamlEditor();

    new ColorPainter();

    expect(colorsOf(editor)).toContain(celColors('cel', 'dark').variable);
    expect(
      colorsOf(editor).every((c) => Object.values(celColors('cel', 'dark')).includes(String(c))),
    ).toBe(true);
  });

  it('leaves editors of other languages alone', () => {
    const editor = new FakeEditor({ languageId: 'json', getText: () => CRD });
    visibleEditors.push(editor);

    new ColorPainter();

    expect(editor.decorations.size).toBe(0);
    expect(decorationTypes).toEqual([]);
  });

  it('clears the colors of an editor that stops being YAML', () => {
    const document = { languageId: 'yaml', getText: () => CRD };
    const editor = new FakeEditor(document);
    visibleEditors.push(editor);
    new ColorPainter();
    expect(colorsOf(editor).length).toBeGreaterThan(0);

    document.languageId = 'json';
    changeVisibleEditors();

    expect(colorsOf(editor)).toEqual([]);
    expect([...editor.decorations.values()].every((ranges) => ranges.length === 0)).toBe(true);
  });

  it('clears the colors of an editor whose document is reopened in another language', () => {
    const document = { languageId: 'yaml', getText: () => CRD };
    const editor = new FakeEditor(document);
    visibleEditors.push(editor);
    new ColorPainter();
    expect(colorsOf(editor).length).toBeGreaterThan(0);

    document.languageId = 'plaintext';
    openDocument(document);

    expect(colorsOf(editor)).toEqual([]);
  });

  it('colors an editor switched from plaintext to yaml without any other event', () => {
    const document = { languageId: 'plaintext', getText: () => CRD };
    const editor = new FakeEditor(document);
    visibleEditors.push(editor);
    new ColorPainter();
    expect(colorsOf(editor)).toEqual([]);

    document.languageId = 'yaml';
    openDocument(document);

    expect(colorsOf(editor)).toContain(celColors('cel', 'dark').variable);
  });

  it('creates no decoration type for off', () => {
    settings.set('cel-lens.colorStyle', 'off');
    const editor = yamlEditor();

    new ColorPainter();

    expect(decorationTypes).toEqual([]);
    expect(editor.decorations.size).toBe(0);
  });

  it('repaints once, after the delay, when a document changes', () => {
    const editor = yamlEditor();
    new ColorPainter();
    const before = decorationTypes.length;
    editor.decorations.clear();

    changeDocument(editor.document);
    changeDocument(editor.document);
    vi.advanceTimersByTime(REPAINT_DELAY_MS - 1);
    expect(editor.decorations.size).toBe(0);
    vi.advanceTimersByTime(1);

    expect(editor.decorations.size).toBe(before);
  });

  describe('in a large CRD', () => {
    const LARGE = [
      'x-kubernetes-validations:',
      ...Array.from({ length: 10000 }, (_, i) => `  - rule: "self.f${String(i)} < 3"`),
      '',
    ].join('\n');

    function paintedLines(editor: FakeEditor): number[] {
      return [...editor.decorations.values()]
        .flat()
        .map((range) => range.startLine)
        .sort((a, b) => a - b);
    }

    it('re-tokenizes only the visible lines and their margin after a one-character edit', () => {
      const editor = yamlEditor(LARGE);
      editor.visibleRanges = [{ start: { line: 5000 }, end: { line: 5040 } }];
      new ColorPainter();
      editor.decorations.clear();

      changeDocument(editor.document);
      vi.advanceTimersByTime(REPAINT_DELAY_MS);

      const lines = paintedLines(editor);
      expect(lines[0]).toBe(5000 - PAINT_MARGIN_LINES);
      expect(lines[lines.length - 1]).toBe(5040 + PAINT_MARGIN_LINES);
      expect(new Set(lines).size).toBe(40 + 2 * PAINT_MARGIN_LINES + 1);
    });

    it('paints the new lines after a scroll out of the painted margin, and not after a short one', () => {
      const editor = yamlEditor(LARGE);
      editor.visibleRanges = [{ start: { line: 500 }, end: { line: 540 } }];
      new ColorPainter();
      editor.decorations.clear();

      scrollEditor(editor, 510, 540);
      vi.advanceTimersByTime(REPAINT_DELAY_MS);
      expect(editor.decorations.size).toBe(0);

      scrollEditor(editor, 8000, 8040);
      vi.advanceTimersByTime(REPAINT_DELAY_MS);
      expect(paintedLines(editor)).toContain(8000);
      expect(paintedLines(editor)).not.toContain(500);
    });
  });

  it('ignores changes to documents that are not YAML, such as an output channel', () => {
    const editor = yamlEditor();
    new ColorPainter();
    editor.decorations.clear();
    const log = { languageId: 'log', getText: () => CRD };

    changeDocument(log);
    vi.advanceTimersByTime(REPAINT_DELAY_MS);

    expect(editor.decorations.size).toBe(0);
  });

  it('re-tokenizes only the YAML document that changed', () => {
    const changed = yamlEditor();
    const other = yamlEditor();
    new ColorPainter();
    changed.decorations.clear();
    other.decorations.clear();
    const read = vi.spyOn(other.document, 'getText');

    changeDocument(changed.document);
    vi.advanceTimersByTime(REPAINT_DELAY_MS);

    expect(changed.decorations.size).toBeGreaterThan(0);
    expect(other.decorations.size).toBe(0);
    expect(read).not.toHaveBeenCalled();
  });

  it('does not repaint visible editors when a document that no editor shows is opened', () => {
    const visible = yamlEditor();
    new ColorPainter();
    const setDecorations = vi.spyOn(visible, 'setDecorations');
    const read = vi.spyOn(visible.document, 'getText');
    const hidden = new FakeEditor({ languageId: 'yaml', getText: () => CRD });

    openDocument(hidden.document);

    expect(setDecorations).not.toHaveBeenCalled();
    expect(read).not.toHaveBeenCalled();
  });

  it('creates no decoration type when a YAML document that no editor shows is opened', () => {
    new ColorPainter();
    const hidden = new FakeEditor({ languageId: 'yaml', getText: () => CRD });

    openDocument(hidden.document);

    expect(decorationTypes).toHaveLength(0);
  });

  it('paints a document change away from the CEL', () => {
    let text = CRD;
    const editor = new FakeEditor({ languageId: 'yaml', getText: () => text });
    visibleEditors.push(editor);
    new ColorPainter();

    text = 'a: 1\n';
    changeDocument(editor.document);
    vi.advanceTimersByTime(REPAINT_DELAY_MS);

    expect(colorsOf(editor)).toEqual([]);
  });

  it('paints an editor that becomes visible', () => {
    new ColorPainter();
    const editor = yamlEditor();

    changeVisibleEditors();

    expect(colorsOf(editor).length).toBeGreaterThan(0);
  });

  it('recolors and disposes the old types when the theme changes', () => {
    const editor = yamlEditor();
    new ColorPainter();
    const old = [...decorationTypes];

    changeTheme(ColorThemeKind.Light);

    expect(old.every((type) => type.disposed)).toBe(true);
    expect(
      colorsOf(editor).every((c) => Object.values(celColors('cel', 'light')).includes(String(c))),
    ).toBe(true);
    expect(colorsOf(editor).length).toBeGreaterThan(0);
  });

  it.each([
    [ColorThemeKind.Light, 'light'],
    [ColorThemeKind.Dark, 'dark'],
    [ColorThemeKind.HighContrast, 'hcDark'],
    [ColorThemeKind.HighContrastLight, 'hcLight'],
  ] as const)('uses the colors of theme kind %s', (kind, name) => {
    changeTheme(kind);
    yamlEditor();

    new ColorPainter();

    expect(decorationTypes.map((type) => type.color)).toContain(celColors('cel', name).variable);
  });

  it('switches style and disposes the old types when the setting changes', () => {
    const editor = yamlEditor();
    new ColorPainter();
    const old = [...decorationTypes];

    settings.set('cel-lens.colorStyle', 'classic');
    changeConfiguration('cel-lens.colorStyle');

    expect(old.every((type) => type.disposed)).toBe(true);
    expect(colorsOf(editor)).toContain(celColors('classic', 'dark').variable);
  });

  it('removes every decoration when the setting becomes off', () => {
    yamlEditor();
    new ColorPainter();
    const old = [...decorationTypes];

    settings.set('cel-lens.colorStyle', 'off');
    changeConfiguration('cel-lens.colorStyle');

    expect(old.every((type) => type.disposed)).toBe(true);
    expect(decorationTypes).toEqual(old);
  });

  it('colors a Kyverno policy only while kyverno is in cel-lens.experimental', () => {
    const editor = yamlEditor(
      'apiVersion: policies.kyverno.io/v1\nkind: ValidatingPolicy\nspec:\n  validations:\n    - expression: object.a < 3\n',
    );
    new ColorPainter();
    expect(colorsOf(editor)).toEqual([]);

    settings.set('cel-lens.experimental', ['kyverno']);
    changeConfiguration('cel-lens.experimental');
    expect(colorsOf(editor)).toContain(celColors('cel', 'dark').variable);

    settings.set('cel-lens.experimental', []);
    changeConfiguration('cel-lens.experimental');
    expect(colorsOf(editor)).toEqual([]);
  });

  it('ignores other settings', () => {
    yamlEditor();
    new ColorPainter();
    const count = decorationTypes.length;

    settings.set('cel-lens.colorStyle', 'off');
    changeConfiguration('cel-lens.trace.server');

    expect(decorationTypes).toHaveLength(count);
    expect(decorationTypes.every((type) => !type.disposed)).toBe(true);
  });

  it('uses a value set only under the old celLens.colorStyle key', () => {
    settings.set('celLens.colorStyle', 'off');
    const editor = yamlEditor();

    new ColorPainter();

    expect(decorationTypes).toEqual([]);
    expect(editor.decorations.size).toBe(0);
  });

  it('prefers cel-lens.colorStyle over the old key', () => {
    settings.set('celLens.colorStyle', 'off');
    settings.set('cel-lens.colorStyle', 'classic');
    yamlEditor();

    new ColorPainter();

    expect(decorationTypes.map((type) => type.color)).toContain(
      celColors('classic', 'dark').variable,
    );
  });

  it('repaints when the old key changes', () => {
    yamlEditor();
    new ColorPainter();
    const count = decorationTypes.length;

    settings.set('celLens.colorStyle', 'off');
    changeConfiguration('celLens.colorStyle');

    expect(decorationTypes.slice(count)).toEqual([]);
    expect(decorationTypes.every((type) => type.disposed)).toBe(true);
  });

  it('falls back to the default style for a value that is not a style', () => {
    settings.set('cel-lens.colorStyle', 'neon');
    yamlEditor();

    new ColorPainter();

    expect(decorationTypes.map((type) => type.color)).toContain(celColors('cel', 'dark').variable);
  });

  it('disposes its types, listeners and pending repaint on dispose', () => {
    const editor = yamlEditor();
    const painter = new ColorPainter();
    changeDocument(editor.document);

    painter.dispose();
    editor.decorations.clear();
    const created = decorationTypes.length;
    vi.advanceTimersByTime(REPAINT_DELAY_MS);
    changeTheme(ColorThemeKind.Light);
    changeVisibleEditors();
    settings.set('cel-lens.colorStyle', 'classic');
    changeConfiguration('cel-lens.colorStyle');
    changeDocument(editor.document);
    vi.advanceTimersByTime(REPAINT_DELAY_MS);

    expect(decorationTypes.slice(0, created).every((type) => type.disposed)).toBe(true);
    expect(decorationTypes).toHaveLength(created);
    expect(editor.decorations.size).toBe(0);
  });
});

describe('ColorPainter in a multi-root workspace', () => {
  afterEach(reset);

  it('paints each editor in the style of its own folder', () => {
    addWorkspaceFolder('a', { 'cel-lens.colorStyle': 'off' });
    addWorkspaceFolder('b');
    const inA = new FakeEditor({
      languageId: 'yaml',
      getText: () => CRD,
      uri: Uri.parse('file:///workspace/a/crd.yaml'),
    });
    const inB = new FakeEditor({
      languageId: 'yaml',
      getText: () => CRD,
      uri: Uri.parse('file:///workspace/b/crd.yaml'),
    });
    visibleEditors.push(inA, inB);

    new ColorPainter();

    expect(colorsOf(inA)).toEqual([]);
    expect(colorsOf(inB)).toContain(celColors('cel', 'dark').variable);
  });
});

describe('chooseColorStyle', () => {
  afterEach(reset);

  it('offers the three styles with a description, the current one marked', async () => {
    settings.set('cel-lens.colorStyle', 'classic');

    await chooseColorStyle();

    expect(quickPicks.shown[0]).toMatchObject([
      { label: 'CEL', description: '' },
      { label: 'Classic', description: '$(check) current' },
      { label: 'Off', description: '' },
    ]);
    expect((quickPicks.shown[0] as { detail: string }[]).every((item) => item.detail !== '')).toBe(
      true,
    );
  });

  it("pre-selects the style of the active editor's folder when it differs from the user setting", async () => {
    settings.set('cel-lens.colorStyle', 'classic');
    addWorkspaceFolder('a', { 'cel-lens.colorStyle': 'off' });
    addWorkspaceFolder('b');
    activeEditor.uri = 'file:///workspace/a/crd.yaml';
    changeActiveEditor('yaml');

    await chooseColorStyle();

    expect(quickPicks.shown[0]).toMatchObject([
      { label: 'CEL', description: '' },
      { label: 'Classic', description: '' },
      { label: 'Off', description: '$(check) current' },
    ]);
  });

  it('highlights the current style when the menu opens, in a single-folder workspace', async () => {
    settings.set('cel-lens.colorStyle', 'classic');
    addWorkspaceFolder('a');

    await chooseColorStyle();

    expect(quickPicks.active[0]).toMatchObject([{ label: 'Classic' }]);
  });

  it("highlights Classic when the active editor's folder says Classic in a two-folder workspace", async () => {
    settings.set('cel-lens.colorStyle', 'off');
    addWorkspaceFolder('a', { 'cel-lens.colorStyle': 'classic' });
    addWorkspaceFolder('b', { 'cel-lens.colorStyle': 'off' });
    activeEditor.uri = 'file:///workspace/a/crd.yaml';
    changeActiveEditor('yaml');

    await chooseColorStyle();

    expect(quickPicks.active[0]).toMatchObject([{ label: 'Classic' }]);
  });

  it('changes no scope when the menu is accepted at once', async () => {
    settings.set('cel-lens.colorStyle', 'off');
    workspaceSettings.set('cel-lens.colorStyle', 'classic');
    addWorkspaceFolder('a', { 'cel-lens.colorStyle': 'classic' });
    addWorkspaceFolder('b', { 'cel-lens.colorStyle': 'off' });
    activeEditor.uri = 'file:///workspace/a/crd.yaml';
    changeActiveEditor('yaml');
    quickPicks.acceptAtOnce = true;

    await chooseColorStyle();

    expect(settingUpdates).toEqual([]);
  });

  it('writes the choice to the user settings', async () => {
    quickPicks.next = 'Off';

    await chooseColorStyle();

    expect(settingUpdates).toEqual([
      { name: 'cel-lens.colorStyle', value: 'off', target: ConfigurationTarget.Global },
    ]);
  });

  it('also writes where the workspace and each folder set the key, so the pick takes effect', async () => {
    addWorkspaceFolder('a', { 'cel-lens.colorStyle': 'classic' });
    addWorkspaceFolder('b');
    workspaceSettings.set('cel-lens.colorStyle', 'classic');
    quickPicks.next = 'Off';

    await chooseColorStyle();

    expect(settingUpdates).toEqual([
      { name: 'cel-lens.colorStyle', value: 'off', target: ConfigurationTarget.Workspace },
      {
        name: 'cel-lens.colorStyle',
        value: 'off',
        target: ConfigurationTarget.WorkspaceFolder,
        folder: 'a',
      },
      { name: 'cel-lens.colorStyle', value: 'off', target: ConfigurationTarget.Global },
    ]);
  });

  it("writes only the open file's folder, leaving the other folder's setting as it is", async () => {
    addWorkspaceFolder('a', { 'cel-lens.colorStyle': 'classic' });
    addWorkspaceFolder('b', { 'cel-lens.colorStyle': 'off' });
    activeEditor.uri = 'file:///workspace/a/crd.yaml';
    changeActiveEditor('yaml');
    quickPicks.next = 'CEL';

    await chooseColorStyle();

    expect(settingUpdates).toEqual([
      {
        name: 'cel-lens.colorStyle',
        value: 'cel',
        target: ConfigurationTarget.WorkspaceFolder,
        folder: 'a',
      },
      { name: 'cel-lens.colorStyle', value: 'cel', target: ConfigurationTarget.Global },
    ]);
  });

  it('writes nothing when the pick is dismissed', async () => {
    await chooseColorStyle();

    expect(settingUpdates).toEqual([]);
  });
});
