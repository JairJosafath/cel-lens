import * as vscode from 'vscode';

import type { CelStyle, ThemeKind } from './cel_styles';
import {
  COLOR_STYLE_SETTING,
  COLOR_STYLE_SETTING_NAME,
  COLOR_STYLE_SETTING_SECTION,
  LEGACY_COLOR_STYLE_SETTING_NAME,
  LEGACY_COLOR_STYLE_SETTING_SECTION,
  parseColorStyle,
  styleItems,
  type StyleItem,
} from './color_style';
import type { LineWindow } from './cel_ranges';
import { planColors } from './color_plan';

/** How long the document must rest before it is painted again. */
export const REPAINT_DELAY_MS = 150;

/** Lines painted above and below the visible ones, so a short scroll shows colors at once. */
export const PAINT_MARGIN_LINES = 100;

/** The lines to paint for an editor: its visible ones and the margin around them. */
function paintWindow(editor: vscode.TextEditor): LineWindow {
  let fromLine = Infinity;
  let toLine = -1;
  for (const range of editor.visibleRanges) {
    fromLine = Math.min(fromLine, range.start.line);
    toLine = Math.max(toLine, range.end.line);
  }
  return {
    fromLine: Math.max(0, fromLine - PAINT_MARGIN_LINES),
    toLine: toLine + PAINT_MARGIN_LINES,
  };
}

function covers(outer: LineWindow, inner: LineWindow): boolean {
  return outer.fromLine <= inner.fromLine && inner.toLine <= outer.toLine;
}

function themeKind(kind: vscode.ColorThemeKind): ThemeKind {
  switch (kind) {
    case vscode.ColorThemeKind.Light:
      return 'light';
    case vscode.ColorThemeKind.Dark:
      return 'dark';
    case vscode.ColorThemeKind.HighContrast:
      return 'hcDark';
    case vscode.ColorThemeKind.HighContrastLight:
      return 'hcLight';
  }
}

/** The value the user set for a color style key, at the narrowest scope that sets it. */
function userValue(section: string, resource?: vscode.Uri): unknown {
  const inspected = vscode.workspace
    .getConfiguration(section, resource)
    .inspect<unknown>(COLOR_STYLE_SETTING);
  return inspected?.workspaceFolderValue ?? inspected?.workspaceValue ?? inspected?.globalValue;
}

/**
 * The color style in force: `cel-lens.colorStyle`, else the old `celLens.colorStyle`.
 * With a `resource`, the folder of a multi-root workspace that holds it counts too.
 */
export function readStyle(resource?: vscode.Uri): CelStyle {
  return parseColorStyle(
    userValue(COLOR_STYLE_SETTING_SECTION, resource) ??
      userValue(LEGACY_COLOR_STYLE_SETTING_SECTION, resource),
  );
}

/** The color style in force for the active editor's folder: what the picker and the actions menu show. */
export function currentColorStyle(): CelStyle {
  return readStyle(vscode.window.activeTextEditor?.document.uri);
}

/**
 * Paints the CEL of the visible lines of the visible YAML editors as text decorations, in the color
 * style and theme kind in force. What to paint comes from `planColors`; this
 * class only talks to VS Code.
 */
export class ColorPainter implements vscode.Disposable {
  private readonly types = new Map<string, vscode.TextEditorDecorationType>();
  /** The editors with colors, and the lines each was last painted for. */
  private readonly painted = new WeakMap<vscode.TextEditor, LineWindow>();
  private paintedWith = '';
  private timer: ReturnType<typeof setTimeout> | undefined;
  private readonly changed = new Set<vscode.TextDocument>();
  private readonly listeners: vscode.Disposable[];

  constructor() {
    this.listeners = [
      vscode.workspace.onDidChangeTextDocument((event) => {
        if (event.document.languageId === 'yaml') {
          this.schedule(event.document);
        }
      }),
      vscode.workspace.onDidOpenTextDocument((document) => {
        this.paint(new Set([document]));
      }),
      vscode.window.onDidChangeVisibleTextEditors(() => {
        this.paint();
      }),
      vscode.window.onDidChangeTextEditorVisibleRanges((event) => {
        const painted = this.painted.get(event.textEditor);
        if (painted !== undefined && !covers(painted, paintWindow(event.textEditor))) {
          this.schedule(event.textEditor.document);
        }
      }),
      vscode.window.onDidChangeActiveColorTheme(() => {
        this.paint();
      }),
      vscode.workspace.onDidChangeConfiguration((event) => {
        if (
          event.affectsConfiguration(COLOR_STYLE_SETTING_NAME) ||
          event.affectsConfiguration(LEGACY_COLOR_STYLE_SETTING_NAME)
        ) {
          this.paint();
        }
      }),
    ];
    this.paint();
  }

  /** Paints the changed documents again once typing pauses. */
  private schedule(document: vscode.TextDocument): void {
    this.changed.add(document);
    clearTimeout(this.timer);
    this.timer = setTimeout(() => {
      this.timer = undefined;
      const documents = new Set(this.changed);
      this.changed.clear();
      this.paint(documents);
    }, REPAINT_DELAY_MS);
  }

  /** Paints the visible YAML editors, or only those of `only` when given. */
  private paint(only?: ReadonlySet<vscode.TextDocument>): void {
    if (only === undefined) {
      clearTimeout(this.timer);
      this.timer = undefined;
      this.changed.clear();
    }
    const theme = themeKind(vscode.window.activeColorTheme.kind);
    const styles = new Map<vscode.TextEditor, CelStyle>();
    for (const editor of vscode.window.visibleTextEditors) {
      if (editor.document.languageId === 'yaml') {
        styles.set(editor, readStyle(editor.document.uri));
      }
    }
    for (const editor of vscode.window.visibleTextEditors) {
      if (editor.document.languageId !== 'yaml' && this.painted.delete(editor)) {
        for (const type of this.types.values()) {
          editor.setDecorations(type, []);
        }
      }
    }
    const key = `${[...styles.values()].join(',')}/${theme}`;
    if (key !== this.paintedWith) {
      this.disposeTypes();
      this.paintedWith = key;
      // Disposing the types clears every editor, so every editor is painted again.
      only = undefined;
    }
    for (const editor of vscode.window.visibleTextEditors) {
      if (editor.document.languageId !== 'yaml') {
        continue;
      }
      if (only !== undefined && !only.has(editor.document)) {
        continue;
      }
      const window = paintWindow(editor);
      this.painted.set(editor, window);
      const ranges = new Map<string, vscode.Range[]>();
      for (const { color, ranges: spans } of planColors(
        editor.document.getText(),
        styles.get(editor) ?? 'cel',
        theme,
        window,
      )) {
        if (!this.types.has(color)) {
          this.types.set(color, vscode.window.createTextEditorDecorationType({ color }));
        }
        ranges.set(
          color,
          spans.map(
            (span) =>
              new vscode.Range(
                span.start.line,
                span.start.character,
                span.end.line,
                span.end.character,
              ),
          ),
        );
      }
      for (const [color, type] of this.types) {
        editor.setDecorations(type, ranges.get(color) ?? []);
      }
    }
  }

  private disposeTypes(): void {
    for (const type of this.types.values()) {
      type.dispose();
    }
    this.types.clear();
  }

  dispose(): void {
    clearTimeout(this.timer);
    this.timer = undefined;
    this.changed.clear();
    for (const listener of this.listeners) {
      listener.dispose();
    }
    this.disposeTypes();
  }
}

/** Shows the styles with the highlight on the current one, so Enter at once changes nothing. */
function pickStyle(current: CelStyle): Promise<StyleItem | undefined> {
  return new Promise((resolve) => {
    const quickPick = vscode.window.createQuickPick<StyleItem>();
    quickPick.title = 'Color style for CEL';
    quickPick.placeholder = 'Pick how cel-lens colors the parts of a CEL expression';
    quickPick.items = styleItems(current);
    quickPick.activeItems = quickPick.items.filter((item) => item.style === current);
    quickPick.onDidAccept(() => {
      resolve(quickPick.selectedItems[0]);
      quickPick.hide();
    });
    quickPick.onDidHide(() => {
      resolve(undefined);
      quickPick.dispose();
    });
    quickPick.show();
  });
}

/**
 * Asks which color style to use, and writes the choice to the user settings and
 * to every wider-than-user scope that sets the key, so the pick is the style in force.
 * With a file open in a workspace folder, only that folder's settings are written;
 * other folders keep theirs.
 */
export async function chooseColorStyle(): Promise<void> {
  const current = currentColorStyle();
  const choice = await pickStyle(current);
  if (choice === undefined || choice.style === current) {
    return;
  }
  const configuration = vscode.workspace.getConfiguration(COLOR_STYLE_SETTING_SECTION);
  if (configuration.inspect<unknown>(COLOR_STYLE_SETTING)?.workspaceValue !== undefined) {
    await configuration.update(
      COLOR_STYLE_SETTING,
      choice.style,
      vscode.ConfigurationTarget.Workspace,
    );
  }
  const uri = vscode.window.activeTextEditor?.document.uri;
  const openFolder = uri === undefined ? undefined : vscode.workspace.getWorkspaceFolder(uri);
  const folders = openFolder === undefined ? vscode.workspace.workspaceFolders : [openFolder];
  for (const folder of folders ?? []) {
    const scoped = vscode.workspace.getConfiguration(COLOR_STYLE_SETTING_SECTION, folder.uri);
    if (scoped.inspect<unknown>(COLOR_STYLE_SETTING)?.workspaceFolderValue !== undefined) {
      await scoped.update(
        COLOR_STYLE_SETTING,
        choice.style,
        vscode.ConfigurationTarget.WorkspaceFolder,
      );
    }
  }
  await configuration.update(COLOR_STYLE_SETTING, choice.style, vscode.ConfigurationTarget.Global);
}
