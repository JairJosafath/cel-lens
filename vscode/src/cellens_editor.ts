import * as vscode from 'vscode';

import type { TextRange } from './value_types';

/**
 * Opens a document, a `.cellens` or an example, in the editor group that already shows it, else
 * beside the active editor, which is the rule's, so the rule stays in view; the focus goes to the
 * selection, and the editor is no preview, so it stays open while the person types.
 */
export async function openBesideRule(document: string, selection: TextRange): Promise<void> {
  const uri = vscode.Uri.parse(document);
  const shown = vscode.window.visibleTextEditors.find(
    (editor) => editor.document.uri.toString() === uri.toString(),
  );
  await vscode.window.showTextDocument(uri, {
    viewColumn: shown?.viewColumn ?? vscode.ViewColumn.Beside,
    selection: new vscode.Range(
      selection.start.line,
      selection.start.character,
      selection.end.line,
      selection.end.character,
    ),
    preserveFocus: false,
    preview: false,
  });
}
