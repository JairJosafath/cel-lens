// The tree of the cel-lens view, as plain data: workspace folders, the documents with rules and
// their rules, drawn from a `RuleList`. It keeps one node object for each id, so VS Code keeps an
// item's expansion and selection across updates, and an update rebuilds only the files an answer
// changed, saying which nodes to refresh. No `vscode` import.
import type { ListChange, ListedDocument, ListedRule, RuleList, ScanLimits } from './rule_list';
import { ruleAtCursor } from './rule_at_cursor';
import {
  documentAccessibleLabel,
  documentDescription,
  documentIcon,
  documentLabel,
  ruleAccessibleLabel,
  ruleDescription,
  ruleIcon,
  type Icon,
} from './rule_items';
import type { RuleLocation } from './rule_parts';

/** What a row is. */
export type NodeKind = 'warning' | 'folder' | 'openFiles' | 'document' | 'unreadable' | 'rule';

/** What clicking a row does: a rule opens its file at the rule; the warning shows the logs. */
export type NodeAction = 'openRule' | 'showLogs';

export interface TreeNode {
  readonly id: string;
  readonly kind: NodeKind;
  label: string;
  description?: string | undefined;
  icon: Icon;
  /** The accessibility label: the row's name and its state in words. */
  accessible: string;
  /** Whether the row opens expanded or collapsed, or has no children. */
  collapsible: 'none' | 'collapsed' | 'expanded';
  /** For the menus: `rule`, `ruleWithExamples`, `document`, `folder`, `openFiles` or `warning`. */
  contextValue: string;
  action?: NodeAction | undefined;
  /** The rule's expression: what clicking, and the menu's commands, take a person to. */
  location?: RuleLocation | undefined;
  rule?: ListedRule | undefined;
  document?: ListedDocument | undefined;
  /** Whether the rule's example words and icon show: false for a folder that turns example results off. */
  showExamples: boolean;
  children: TreeNode[];
  parent: TreeNode | undefined;
}

/** A workspace folder, as the tree needs it. */
export interface TreeFolder {
  readonly uri: string;
  readonly name: string;
}

/** What the tree is drawn with besides the list: the folders, and what each folder's settings say. */
export interface TreeContext {
  /** The workspace folders, in the workspace's order. */
  readonly folders: readonly TreeFolder[];
  /** The same URI in one spelling, as VS Code and the engine may write it differently. */
  readonly normalize: (uri: string) => string;
  /** Whether example results are on for the file at `uri`, which is in the folder `folder`, if any. */
  readonly showExamples: (uri: string) => boolean;
  readonly limits: ScanLimits;
  /** Only the rules with a problem show, with their documents and folders. */
  readonly problemsOnly: boolean;
}

/** What to refresh after an update: the nodes whose children or own row changed; `undefined` is the root. */
export type Refresh = readonly (TreeNode | undefined)[];

const OPEN_FILES_ID = 'cel-lens.openFiles';
const FILES_LEFT_OUT_ID = 'cel-lens.filesLeftOut';
const UNREADABLE_ID = 'cel-lens.unreadable';

/** A byte count as the engine's limits are written: `1 MiB`. */
function byteWords(bytes: number): string {
  const mib = 1024 * 1024;
  if (bytes >= mib && bytes % mib === 0) {
    return `${String(bytes / mib)} MiB`;
  }
  if (bytes >= 1024 && bytes % 1024 === 0) {
    return `${String(bytes / 1024)} KiB`;
  }
  return `${String(bytes)} bytes`;
}

function number(value: number): string {
  return value.toLocaleString('en-US');
}

/** The first row when the scan left files out: how many, and the limits that left them out. */
export function filesLeftOutText(filesLeftOut: number, limits: ScanLimits): string {
  const read =
    limits.files === undefined || limits.fileBytes === undefined
      ? "the scan's limits left them out"
      : `the scan reads at most ${number(limits.files)} files of at most ${byteWords(limits.fileBytes)}`;
  return `${number(filesLeftOut)} YAML files not read: ${read}`;
}

/** The id of a document's node: its file and its name in the engine's words, and `#2` for the second of one name in a file. */
function documentId(document: ListedDocument, occurrence: number): string {
  return `${document.uri}#${document.document}${occurrence > 1 ? `#${String(occurrence)}` : ''}`;
}

/** Which of the documents of that name in the file this is, counting from 1, in the file's order. */
function occurrenceIn(documents: readonly ListedDocument[], document: ListedDocument): number {
  let times = 0;
  for (const each of documents) {
    if (each.document === document.document) {
      times += 1;
    }
    if (each === document) {
      break;
    }
  }
  return times;
}

/** The row for entries of the engine's list that have no file to show a warning in: how many, and where to read more. */
export function unreadableText(count: number): string {
  return count === 1
    ? '1 entry of the engine’s list could not be read'
    : `${number(count)} entries of the engine’s list could not be read`;
}

/** The message of the view while the engine scans: how far it is. */
export function scanMessage(yamlFiles: number, filesWithRules: number): string {
  return `Scanning: ${number(yamlFiles)} YAML files read, ${number(filesWithRules)} with rules`;
}

function node(id: string, kind: NodeKind, label: string, over: Partial<TreeNode> = {}): TreeNode {
  return {
    id,
    kind,
    label,
    icon: { id: 'circle-outline' },
    accessible: label,
    collapsible: 'none',
    contextValue: kind,
    showExamples: true,
    children: [],
    parent: undefined,
    ...over,
  };
}

/** Whether two lists hold the same nodes, in the same order. */
function sameNodes(a: readonly TreeNode[], b: readonly TreeNode[]): boolean {
  return a.length === b.length && a.every((each, at) => each === b[at]);
}

/** The tree. It holds the nodes; `update` and `rebuild` keep them current and say what to refresh. */
export class RuleTree {
  private context: TreeContext;
  private readonly byId = new Map<string, TreeNode>();
  /** The nodes of the documents of each file, in the file's order. */
  private readonly files = new Map<string, TreeNode[]>();
  /** The ids of every node drawn for each file, its documents and their rules, to drop the ones a redraw leaves out. */
  private readonly ids = new Map<string, Set<string>>();
  /** What each file was drawn from, to skip a file an answer did not really change. */
  private readonly signatures = new Map<string, string>();
  /** Where each file's documents go and which they are, to tell a change of rows from a change of order. */
  private readonly shapes = new Map<string, string>();
  private readonly folderNodes = new Map<string, TreeNode>();
  private openFiles: TreeNode | undefined;
  private warning: TreeNode | undefined;
  private unreadableRow: TreeNode | undefined;
  private rootChildren: TreeNode[] = [];

  constructor(
    private readonly list: RuleList,
    context: TreeContext,
  ) {
    this.context = context;
  }

  /** The rows at the top. */
  roots(): TreeNode[] {
    return this.rootChildren;
  }

  /** Whether there is no row at all: nothing to list. */
  get isEmpty(): boolean {
    return this.rootChildren.length === 0;
  }

  /** The node with this id, if the tree has it. */
  find(id: string): TreeNode | undefined {
    return this.byId.get(id);
  }

  /**
   * Draws everything again, for a new context (the folders, or a setting) or a replaced list. The
   * nodes it had are kept for their ids, so what a person expanded stays expanded.
   */
  rebuild(context: TreeContext = this.context): Refresh {
    this.context = context;
    this.signatures.clear();
    this.shapes.clear();
    const gone = new Set(this.files.keys());
    for (const uri of this.list.files) {
      gone.delete(uri);
      this.drawFile(uri);
    }
    for (const uri of gone) {
      this.drawFile(uri);
    }
    this.arrange();
    return [undefined];
  }

  /**
   * Draws the files a change touched, and only those, and says what to refresh: the nodes of the
   * documents whose rules changed, and a parent whose documents came, went or moved.
   */
  update(change: ListChange): Refresh {
    if (change.stale) {
      return [];
    }
    const touched = new Set<TreeNode>();
    let reshaped = change.full;
    for (const uri of change.files) {
      const drawn = this.drawFile(uri);
      if (drawn !== 'same') {
        for (const each of this.files.get(uri) ?? []) {
          touched.add(each);
        }
      }
      reshaped ||= drawn === 'shape';
    }
    // Only a file whose documents came, went or moved can change a group's rows or their order;
    // for any other change the groups keep their children, and only the documents are refreshed.
    const parents = reshaped ? this.arrange() : new Set<TreeNode | undefined>();
    if (parents.has(undefined)) {
      return [undefined];
    }
    const refresh = new Set<TreeNode>();
    for (const parent of parents) {
      if (parent !== undefined) {
        refresh.add(parent);
      }
    }
    for (const each of touched) {
      if (each.parent === undefined || !refresh.has(each.parent)) {
        refresh.add(each);
      }
    }
    return [...refresh].filter((each) => this.byId.has(each.id));
  }

  /** Draws the row of files left out, or takes it away; says what to refresh. */
  setFilesLeftOut(filesLeftOut: number): Refresh {
    const text = filesLeftOut > 0 ? filesLeftOutText(filesLeftOut, this.context.limits) : undefined;
    if (text === this.warning?.label) {
      return [];
    }
    if (text === undefined) {
      this.warning = undefined;
    } else {
      this.warning = this.upsert(FILES_LEFT_OUT_ID, 'warning', text, {
        icon: { id: 'warning', color: 'problemsWarningIcon.foreground' },
        accessible: `Warning: ${text}. Show the logs.`,
        action: 'showLogs',
        contextValue: 'warning',
      });
    }
    this.arrange();
    return [undefined];
  }

  /** Draws the row of entries that have no file, or takes it away; says what to refresh. */
  setUnreadable(count: number): Refresh {
    const text = count > 0 ? unreadableText(count) : undefined;
    if (text === this.unreadableRow?.label) {
      return [];
    }
    this.unreadableRow =
      text === undefined
        ? undefined
        : this.upsert(UNREADABLE_ID, 'warning', text, {
            icon: { id: 'warning', color: 'problemsWarningIcon.foreground' },
            accessible: `Warning: ${text}. Show the logs.`,
            action: 'showLogs',
            contextValue: 'warning',
          });
    this.arrange();
    return [undefined];
  }

  /**
   * The row the cursor is in: the rule whose span holds the position, else the document whose range
   * does, in the file `uri` (as the engine spells it). A rule the filter hides is answered by its
   * document, when that shows; `undefined` when no row holds it.
   */
  at(uri: string, line: number, character: number): TreeNode | undefined {
    const documents = this.list.documentsOf(uri);
    const found = ruleAtCursor(documents, line, character);
    if (found === undefined) {
      return undefined;
    }
    const rule = found.rule === undefined ? undefined : this.byId.get(found.rule.id);
    // The document is found by its id, which the list's document has whatever object it is: an
    // answer that sends a file unchanged gives new objects and leaves the nodes as they were.
    return (
      rule ?? this.byId.get(documentId(found.document, occurrenceIn(documents, found.document)))
    );
  }

  /** The node of this id, made again from `over` when it exists, so VS Code's handle on it stays good. */
  private upsert(id: string, kind: NodeKind, label: string, over: Partial<TreeNode>): TreeNode {
    const fresh = node(id, kind, label, over);
    const old = this.byId.get(id);
    if (old?.kind !== kind) {
      this.byId.set(id, fresh);
      return fresh;
    }
    old.label = fresh.label;
    old.icon = fresh.icon;
    old.accessible = fresh.accessible;
    old.collapsible = fresh.collapsible;
    old.contextValue = fresh.contextValue;
    old.showExamples = fresh.showExamples;
    old.description = fresh.description;
    old.action = fresh.action;
    old.location = fresh.location;
    old.rule = fresh.rule;
    old.document = fresh.document;
    if (over.children !== undefined) {
      old.children = over.children;
    }
    return old;
  }

  /** The workspace folder holding a document, if one of the workspace's does. */
  private folderOf(document: ListedDocument): TreeFolder | undefined {
    if (document.folder === undefined) {
      return undefined;
    }
    const wanted = this.context.normalize(document.folder);
    return this.context.folders.find((each) => this.context.normalize(each.uri) === wanted);
  }

  /**
   * Where a document goes: the node of its workspace folder (with several folders), the root (with
   * one), or *Open files*, which a file in no workspace folder is under.
   */
  private groupOf(document: ListedDocument): 'root' | TreeFolder | 'open' {
    const folder = this.folderOf(document);
    if (folder === undefined) {
      return 'open';
    }
    return this.context.folders.length > 1 ? folder : 'root';
  }

  private folderNode(folder: TreeFolder): TreeNode {
    const node = this.upsert(`cel-lens.folder:${folder.uri}`, 'folder', folder.name, {
      icon: { id: 'folder' },
      collapsible: 'expanded',
      accessible: `${folder.name}, workspace folder`,
    });
    this.folderNodes.set(node.id, node);
    return node;
  }

  /**
   * Draws a file's documents from the list, reusing the nodes it had. Says whether anything of it
   * changed since it was last drawn, and whether the change is one of shape: its documents came,
   * went, moved to another group or changed their ids, which changes a group's rows.
   */
  private drawFile(uri: string): 'same' | 'content' | 'shape' {
    const all = this.list.documentsOf(uri);
    const first = all[0];
    const showExamples = first === undefined ? true : this.context.showExamples(uri);
    const group = first === undefined ? '' : this.groupKey(first);
    const signature = JSON.stringify([showExamples, group, this.context.problemsOnly, all]);
    if (this.signatures.get(uri) === signature) {
      return 'same';
    }
    // With the filter on, a document shows its rules with a problem, and none with no such rule.
    // The number of a document among those of its name counts all of them, so its id is the same with the filter on or off.
    const seen = new Map<string, number>();
    const shown = all.flatMap((original) => {
      const occurrence = (seen.get(original.document) ?? 0) + 1;
      seen.set(original.document, occurrence);
      if (!this.context.problemsOnly || original.unreadable === true) {
        return [{ occurrence, shown: original }];
      }
      const rules = original.rules.filter((rule) => rule.problem);
      return rules.length === 0 ? [] : [{ occurrence, shown: { ...original, rules } }];
    });
    const keep = new Set<string>();
    const nodes = shown.map(({ occurrence, shown: document }) => {
      const drawn = this.documentNode(document, occurrence, showExamples);
      keep.add(drawn.id);
      for (const child of drawn.children) {
        keep.add(child.id);
      }
      return drawn;
    });
    for (const id of this.ids.get(uri) ?? []) {
      if (!keep.has(id)) {
        this.byId.delete(id);
      }
    }
    const shape = nodes.length === 0 ? '' : `${group}\n${nodes.map((each) => each.id).join('\n')}`;
    const earlier = this.shapes.get(uri) ?? '';
    if (nodes.length === 0) {
      this.files.delete(uri);
      this.ids.delete(uri);
      this.signatures.delete(uri);
      this.shapes.delete(uri);
    } else {
      this.files.set(uri, nodes);
      this.ids.set(uri, keep);
      this.signatures.set(uri, signature);
      this.shapes.set(uri, shape);
    }
    return shape === earlier ? 'content' : 'shape';
  }

  private groupKey(document: ListedDocument): string {
    const group = this.groupOf(document);
    return typeof group === 'string' ? group : group.uri;
  }

  private documentNode(
    document: ListedDocument,
    occurrence: number,
    showExamples: boolean,
  ): TreeNode {
    const id = documentId(document, occurrence);
    if (document.unreadable === true) {
      const start = document.range?.start ?? { line: 0, character: 0 };
      return this.upsert(id, 'unreadable', document.name, {
        description: document.file,
        icon: { id: 'warning', color: 'problemsWarningIcon.foreground' },
        accessible: `Warning: ${document.name}, in ${document.file}. It counts as a problem.`,
        contextValue: 'unreadable',
        action: 'openRule',
        location: { uri: document.uri, range: { start, end: start } },
        document,
        showExamples,
      });
    }
    const problems = document.rules.filter((rule) => rule.problem).length;
    const drawn = this.upsert(id, 'document', documentLabel(document), {
      description: documentDescription(document),
      icon: documentIcon(document.kind),
      collapsible: 'collapsed',
      accessible: documentAccessibleLabel(document, problems),
      document,
      showExamples,
    });
    drawn.children = document.rules.map((rule) =>
      this.ruleNode(rule, document, showExamples, drawn),
    );
    return drawn;
  }

  private ruleNode(
    rule: ListedRule,
    document: ListedDocument,
    showExamples: boolean,
    parent: TreeNode,
  ): TreeNode {
    const shown = showExamples && rule.examples !== undefined;
    const drawn = this.upsert(rule.id, 'rule', rule.label, {
      description: ruleDescription(rule, showExamples),
      icon: ruleIcon(rule, showExamples),
      accessible: ruleAccessibleLabel(rule, showExamples),
      contextValue: shown ? 'ruleWithExamples' : 'rule',
      action: 'openRule',
      location: rule.location,
      rule,
      document,
      showExamples,
    });
    drawn.parent = parent;
    return drawn;
  }

  /**
   * Sets the children of the groups, and the root's rows, from the list's order, and says which
   * parents got other children: `undefined` is the root.
   */
  private arrange(): Set<TreeNode | undefined> {
    const top: TreeNode[] = [];
    const inFolder = new Map<string, TreeNode[]>();
    const open: TreeNode[] = [];
    for (const uri of this.list.files) {
      for (const each of this.files.get(uri) ?? []) {
        const group = each.document === undefined ? 'open' : this.groupOf(each.document);
        if (group === 'root') {
          top.push(each);
        } else if (group === 'open') {
          open.push(each);
        } else {
          const nodes = inFolder.get(group.uri);
          if (nodes === undefined) {
            inFolder.set(group.uri, [each]);
          } else {
            nodes.push(each);
          }
        }
      }
    }
    const parents = new Set<TreeNode | undefined>();
    const folders: TreeNode[] = [];
    for (const folder of this.context.folders) {
      const nodes = inFolder.get(folder.uri);
      if (nodes !== undefined) {
        const drawn = this.folderNode(folder);
        this.setChildren(drawn, nodes, parents);
        folders.push(drawn);
      }
    }
    for (const [id, folder] of this.folderNodes) {
      if (!folders.includes(folder)) {
        this.folderNodes.delete(id);
        this.byId.delete(id);
      }
    }
    if (open.length === 0) {
      if (this.openFiles !== undefined) {
        this.byId.delete(this.openFiles.id);
        this.openFiles = undefined;
      }
    } else {
      this.openFiles = this.upsert(OPEN_FILES_ID, 'openFiles', 'Open files', {
        icon: { id: 'files' },
        collapsible: 'expanded',
        accessible: 'Open files, files outside the workspace folders',
      });
      this.setChildren(this.openFiles, open, parents);
    }
    const next = [
      ...(this.warning === undefined ? [] : [this.warning]),
      ...(this.unreadableRow === undefined ? [] : [this.unreadableRow]),
      ...(this.context.folders.length > 1 ? folders : top),
      ...(this.openFiles === undefined ? [] : [this.openFiles]),
    ];
    for (const each of top) {
      each.parent = undefined;
    }
    for (const each of next) {
      if (each.kind !== 'document' && each.kind !== 'unreadable') {
        each.parent = undefined;
      }
    }
    if (!sameNodes(next, this.rootChildren)) {
      parents.add(undefined);
    }
    this.rootChildren = next;
    return parents;
  }

  private setChildren(
    parent: TreeNode,
    children: TreeNode[],
    parents: Set<TreeNode | undefined>,
  ): void {
    if (!sameNodes(parent.children, children)) {
      parents.add(parent);
    }
    parent.children = children;
    for (const each of children) {
      each.parent = parent;
    }
  }
}
