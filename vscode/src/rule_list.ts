// The model of the cel-lens view: the rules of the workspace as the engine's `cel-lens.ruleList`
// lists them, kept current by answers. It reads no YAML and works no state out; it holds what the
// engine said, file by file, and says which files an answer changed. No `vscode` import: it is
// tested in plain Node, and the tree is drawn from it.
import { isRecord } from './is_record';
import type { Range, RuleLocation } from './rule_parts';

/** The engine's command that lists the rules of the workspace. */
export const ENGINE_RULE_LIST = 'cel-lens.ruleList';

/** The notification with which the engine says the list changed; it never carries the list. */
export const RULE_LIST_CHANGED_NOTIFICATION = 'cel-lens/ruleListChanged';

/** What a rule's CEL does when compiled: the state of the file's check at the rule. */
export type CompileState = 'ok' | 'warning' | 'error';

/** One of the first problems of a rule. */
export interface RuleMessage {
  readonly message: string;
  readonly severity: 'error' | 'warning' | 'information' | 'hint';
}

/** A rule's compile state, the count of its problems and its first messages. */
export interface Compile {
  readonly state: CompileState;
  readonly problems: number;
  readonly messages: readonly RuleMessage[];
}

/** What a rule's examples say, in the words of the line above the rule. */
export type ExampleState = 'wrong' | 'error' | 'pass' | 'notRun';

export interface Examples {
  readonly state: ExampleState;
  /** The part of the line, such as `refuses 1 example it should allow · 2 pass`. */
  readonly words: string;
}

/** One CEL rule of a document. */
export interface ListedRule {
  /** The engine's: it names the rule by its file, document and place, so it survives edits. */
  readonly id: string;
  readonly label: string;
  /** Where the label came from: `message`, `name`, `key` or `expression`. */
  readonly labelFrom: string;
  /** Where the rule's expression is: the argument of `cel-lens.showRule`. */
  readonly location: RuleLocation;
  readonly span?: Range;
  /** A CRD's `version` and `path`, a policy's `field` and `name`; the rule's `index` in its list. */
  readonly version?: string;
  readonly path?: string;
  readonly field?: string;
  readonly name?: string;
  readonly index: number;
  readonly compile: Compile;
  /** `undefined` when the engine says none: no example, no example results for the kind, or off. */
  readonly examples?: Examples;
  /** Whether the rule has a problem: the engine sets it, so the badge and the filter count one thing. */
  readonly problem: boolean;
  /** The rule's CEL, when the engine sends it. */
  readonly expression?: string;
  /** This is no rule of the engine's but the place of one the extension could not read: it shows as a warning and counts as a problem. */
  readonly unreadable?: true;
}

/** A document with rules: a CRD or a policy, in a file. */
export interface ListedDocument {
  readonly uri: string;
  /** The URI of the deepest workspace folder holding the file; none for an open file outside them. */
  readonly folder?: string;
  /** The file's path below that folder. */
  readonly file: string;
  /** The engine's name for it, such as `CustomResourceDefinition widgets.example.com`. */
  readonly document: string;
  readonly kind: string;
  readonly name: string;
  readonly range?: Range;
  readonly rules: readonly ListedRule[];
  /** This is no document of the engine's but the place of one the extension could not read: it shows as a warning and counts as a problem. */
  readonly unreadable?: true;
}

/** How far the engine's walk of the workspace is. */
export interface Scan {
  readonly state: 'scanning' | 'done';
  readonly yamlFiles: number;
  readonly filesWithRules: number;
  readonly filesLeftOut: number;
}

/** The scan of an engine that says nothing of it: the first answer waits for the walk, so it is done. */
export const DONE_SCAN: Scan = { state: 'done', yamlFiles: 0, filesWithRules: 0, filesLeftOut: 0 };

/** An answer of `cel-lens.ruleList`, read. */
export interface RuleListAnswer {
  readonly kubernetesVersion?: string;
  /** Grows each time a file's entry changes; none for an engine that answers the whole list each time. */
  readonly generation?: number;
  /** The answer holds the whole list, whatever the client has. */
  readonly full: boolean;
  readonly documents: readonly ListedDocument[];
  /** How many entries of the answer were not read, those placed in a file as a warning row and those with no file. */
  readonly skipped: number;
  /** How many entries were not read and have no file to show a warning row in: they are one row at the top, and count as problems. */
  readonly unattributed: number;
  /** The URIs of the files listed before and not now. */
  readonly removed: readonly string[];
  readonly scan?: Scan;
}

/** The notification `cel-lens/ruleListChanged`, read. */
export interface RuleListChange {
  readonly generation?: number;
  readonly scan?: Scan;
}

function text(value: unknown): string | undefined {
  return typeof value === 'string' && value !== '' ? value : undefined;
}

function count(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isInteger(value) && value >= 0 ? value : undefined;
}

function position(value: unknown): { line: number; character: number } | undefined {
  if (!isRecord(value)) {
    return undefined;
  }
  const line = count(value['line']);
  const character = count(value['character']);
  return line === undefined || character === undefined ? undefined : { line, character };
}

function range(value: unknown): Range | undefined {
  if (!isRecord(value)) {
    return undefined;
  }
  const start = position(value['start']);
  const end = position(value['end']);
  return start === undefined || end === undefined ? undefined : { start, end };
}

function severity(value: unknown): RuleMessage['severity'] {
  return value === 'warning' || value === 'information' || value === 'hint' ? value : 'error';
}

function parseCompile(value: unknown): Compile | undefined {
  if (!isRecord(value)) {
    return undefined;
  }
  const state = value['state'];
  if (state !== 'ok' && state !== 'warning' && state !== 'error') {
    return undefined;
  }
  const messages = Array.isArray(value['messages'])
    ? value['messages'].flatMap((entry) =>
        isRecord(entry) && text(entry['message']) !== undefined
          ? [{ message: String(entry['message']), severity: severity(entry['severity']) }]
          : [],
      )
    : [];
  return { state, problems: count(value['problems']) ?? messages.length, messages };
}

function parseExamples(value: unknown): Examples | undefined {
  if (!isRecord(value)) {
    return undefined;
  }
  const state = value['state'];
  const words = text(value['words']);
  return (state === 'wrong' || state === 'error' || state === 'pass' || state === 'notRun') &&
    words !== undefined
    ? { state, words }
    : undefined;
}

/** What the engine would have said of a rule, as far as it can be told, to place a warning row where the rule is. */
interface Where {
  /** The document's file. */
  readonly uri: string;
  /** The document's id, for the id of a warning row. */
  readonly document: string;
  readonly range?: Range;
}

/** The warning row for a rule the extension could not read: it says so, opens the file where the rule is, and is a problem. */
function unreadableRule(value: unknown, index: number, where: Where): ListedRule {
  const record = isRecord(value) ? value : {};
  const location = isRecord(record['location']) ? record['location'] : {};
  const uri = text(location['uri']) ?? where.uri;
  const here = range(location['range']) ??
    where.range ?? { start: { line: 0, character: 0 }, end: { line: 0, character: 0 } };
  const span = range(record['span']);
  return {
    id: text(record['id']) ?? `${where.uri}#${where.document}#unreadable#${String(index)}`,
    label: 'A rule that could not be read',
    labelFrom: 'unreadable',
    location: { uri, range: here },
    ...(span !== undefined && { span }),
    index,
    compile: {
      state: 'warning',
      problems: 1,
      messages: [
        {
          message:
            'cel-lens could not read this rule in the engine’s answer; the engine may not match this extension.',
          severity: 'warning',
        },
      ],
    },
    problem: true,
    unreadable: true,
  };
}

function parseRule(value: unknown, index: number): ListedRule | undefined {
  if (!isRecord(value)) {
    return undefined;
  }
  const id = text(value['id']);
  const uri = isRecord(value['location']) ? text(value['location']['uri']) : undefined;
  const here = isRecord(value['location']) ? range(value['location']['range']) : undefined;
  const compile = parseCompile(value['compile']);
  if (id === undefined || uri === undefined || here === undefined || compile === undefined) {
    return undefined;
  }
  const span = range(value['span']);
  const examples = parseExamples(value['examples']);
  const expression = text(value['expression']);
  const version = text(value['version']);
  const path = typeof value['path'] === 'string' ? value['path'] : undefined;
  const field = text(value['field']);
  const name = text(value['name']);
  return {
    id,
    label: text(value['label']) ?? id,
    labelFrom: text(value['labelFrom']) ?? 'expression',
    location: { uri, range: here },
    ...(span !== undefined && { span }),
    ...(version !== undefined && { version }),
    ...(path !== undefined && { path }),
    ...(field !== undefined && { field }),
    ...(name !== undefined && { name }),
    index: count(value['index']) ?? index,
    compile,
    ...(examples !== undefined && { examples }),
    problem:
      typeof value['problem'] === 'boolean'
        ? value['problem']
        : compile.state !== 'ok' || examples?.state === 'wrong' || examples?.state === 'error',
    ...(expression !== undefined && { expression }),
  };
}

/** A document as read, and how many of its entries were not read. */
interface ReadDocument {
  readonly document: ListedDocument;
  readonly skipped: number;
}

/**
 * The warning row for a document the extension could not read, in the file the engine gave it. It
 * has no rules: it is itself the row, and a problem. `n` tells apart two of one file.
 */
function unreadableDocument(value: Record<string, unknown>, uri: string): ListedDocument {
  const folder = text(value['folder']);
  const where = range(value['range']);
  return {
    uri,
    ...(folder !== undefined && { folder }),
    file: text(value['file']) ?? uri,
    document: 'unreadable',
    kind: text(value['kind']) ?? 'unreadable',
    name: 'A document that could not be read',
    ...(where !== undefined && { range: where }),
    rules: [],
    unreadable: true,
  };
}

/**
 * Reads a document. Nothing the engine lists is dropped: a rule that is not read becomes a
 * warning row in its document, and a document that is not read becomes a warning row in its file.
 * `undefined` only for an entry with no file to put the row in.
 */
function parseDocument(value: unknown): ReadDocument | undefined {
  if (!isRecord(value)) {
    return undefined;
  }
  const uri = text(value['uri']);
  if (uri === undefined) {
    return undefined;
  }
  const kind = text(value['kind']);
  if (kind === undefined || !Array.isArray(value['rules'])) {
    return { document: unreadableDocument(value, uri), skipped: 1 };
  }
  // A document with no `metadata.name` is listed with an empty name: the CRD being written, before it is named.
  const name = typeof value['name'] === 'string' ? value['name'] : '';
  const folder = text(value['folder']);
  const where = range(value['range']);
  const id = text(value['document']) ?? `${kind} ${name}`;
  const place: Where = { uri, document: id, ...(where !== undefined && { range: where }) };
  let skipped = 0;
  const rules = value['rules'].map((entry, index) => {
    const rule = parseRule(entry, index);
    if (rule !== undefined) {
      return rule;
    }
    skipped += 1;
    return unreadableRule(entry, index, place);
  });
  return {
    skipped,
    document: {
      uri,
      ...(folder !== undefined && { folder }),
      file: text(value['file']) ?? uri,
      document: id,
      kind,
      name,
      ...(where !== undefined && { range: where }),
      rules,
    },
  };
}

function parseScan(value: unknown): Scan | undefined {
  if (!isRecord(value) || (value['state'] !== 'scanning' && value['state'] !== 'done')) {
    return undefined;
  }
  return {
    state: value['state'],
    yamlFiles: count(value['yamlFiles']) ?? 0,
    filesWithRules: count(value['filesWithRules']) ?? 0,
    filesLeftOut: count(value['filesLeftOut']) ?? 0,
  };
}

/**
 * Reads an answer of `cel-lens.ruleList`: `undefined` when it is no list this extension reads (not
 * an object with documents), so the list it has stays as it was. An entry that is not read is never
 * dropped: it is a warning row where it was (a rule in its document, a document in its file) that
 * counts as a problem, or, with no file, one of `unattributed`; `skipped` counts them all.
 */
export function parseRuleListAnswer(value: unknown): RuleListAnswer | undefined {
  if (!isRecord(value) || !Array.isArray(value['documents'])) {
    return undefined;
  }
  const read = value['documents'].map(parseDocument);
  const documents = read.filter((each) => each !== undefined);
  const removed = Array.isArray(value['removed'])
    ? value['removed'].filter((uri): uri is string => typeof uri === 'string')
    : [];
  const generation = count(value['generation']);
  const scan = parseScan(value['scan']);
  const version = text(value['kubernetesVersion']);
  return {
    ...(version !== undefined && { kubernetesVersion: version }),
    ...(generation !== undefined && { generation }),
    full: value['full'] === true,
    documents: documents.map((each) => each.document),
    skipped:
      read.length - documents.length + documents.reduce((total, each) => total + each.skipped, 0),
    unattributed: read.length - documents.length,
    removed,
    ...(scan !== undefined && { scan }),
  };
}

/** Reads the notification `cel-lens/ruleListChanged`, `{generation, scan}`; `undefined` for anything else. */
export function parseRuleListChange(value: unknown): RuleListChange | undefined {
  if (!isRecord(value)) {
    return undefined;
  }
  const generation = count(value['generation']);
  const scan = parseScan(value['scan']);
  return generation === undefined && scan === undefined
    ? undefined
    : { ...(generation !== undefined && { generation }), ...(scan !== undefined && { scan }) };
}

/** The limits of the engine's scan, from the `initialize` result: files read, and the most bytes of one. */
export interface ScanLimits {
  readonly files?: number;
  readonly fileBytes?: number;
}

/** Reads `capabilities.experimental.celLens.workspaceScanMaxFiles` and `workspaceScanMaxFileBytes`. */
export function parseScanLimits(initializeResult: unknown): ScanLimits {
  const capabilities = isRecord(initializeResult) ? initializeResult['capabilities'] : undefined;
  const experimental = isRecord(capabilities) ? capabilities['experimental'] : undefined;
  const engine = isRecord(experimental) ? experimental['celLens'] : undefined;
  if (!isRecord(engine)) {
    return {};
  }
  const files = count(engine['workspaceScanMaxFiles']);
  const fileBytes = count(engine['workspaceScanMaxFileBytes']);
  return {
    ...(files !== undefined && { files }),
    ...(fileBytes !== undefined && { fileBytes }),
  };
}

/** What an answer changed, for the tree to fire only for that. */
export interface ListChange {
  /** The files whose documents were added, replaced or dropped, in no order. */
  readonly files: readonly string[];
  /** Whether the answer replaced the whole list. */
  readonly full: boolean;
  /** The answer was older than the list and was left out. */
  readonly stale: boolean;
}

/** The first of two files that sorts after the other among files of one folder: the engine orders them by path. */
function precedes(a: ListedDocument, b: ListedDocument): boolean {
  return a.file < b.file;
}

/**
 * The rules of the workspace, file by file. An answer replaces what it holds for a file; with
 * `full`, or from an engine that knows no generations, it replaces the whole list; `removed`
 * drops files. The files keep the engine's order: the order of a whole answer, and a new file of a
 * later answer among the files of its folder by path.
 */
export class RuleList {
  private readonly byFile = new Map<string, ListedDocument[]>();
  /** How many rules with a problem each file has, so the total follows an answer without a walk of the list. */
  private readonly problemsOf = new Map<string, number>();
  private problemTotal = 0;
  private unattributedCount = 0;
  private order: string[] = [];
  private current: number | undefined;
  private lastScan: Scan = DONE_SCAN;
  private unreadWarned = false;
  /** Whether an answer was ever applied since the last reset. */
  private answered = false;

  /** The generation of the last answer, to ask what changed `since` it; none from an engine without. */
  get generation(): number | undefined {
    return this.current;
  }

  get scan(): Scan {
    return this.lastScan;
  }

  /** Whether an answer was applied since the engine started. */
  get hasAnswer(): boolean {
    return this.answered;
  }

  /**
   * How many rules have a problem, whatever a filter shows: the engine's `problem` of each rule, a
   * warning row for each entry that was not read, and what has no file to show a row in.
   */
  get problems(): number {
    return this.problemTotal + this.unattributedCount;
  }

  /** How many entries the engine listed that have no file to show a warning row in. */
  get unattributed(): number {
    return this.unattributedCount;
  }

  /** The URIs of the files with rules, in the engine's order. */
  get files(): readonly string[] {
    return this.order;
  }

  /** The documents of a file, in the file's order. */
  documentsOf(uri: string): readonly ListedDocument[] {
    return this.byFile.get(uri) ?? [];
  }

  /** How many rules, and how many documents, the list holds. */
  get totals(): { documents: number; rules: number } {
    let documents = 0;
    let rules = 0;
    for (const list of this.byFile.values()) {
      documents += list.length;
      for (const document of list) {
        rules += document.rules.length;
      }
    }
    return { documents, rules };
  }

  /** Starts over: a restarted engine knows no generation of ours. */
  reset(): ListChange {
    const files = this.order;
    this.byFile.clear();
    this.problemsOf.clear();
    this.problemTotal = 0;
    this.unattributedCount = 0;
    this.order = [];
    this.current = undefined;
    this.lastScan = DONE_SCAN;
    this.unreadWarned = false;
    this.answered = false;
    return { files, full: true, stale: false };
  }

  /**
   * Says whether an answer that cannot be read should be logged: once, until the next reset or
   * answer that can be.
   */
  shouldLogUnread(): boolean {
    if (this.unreadWarned) {
      return false;
    }
    this.unreadWarned = true;
    return true;
  }

  /** Takes the scan the notification gave. */
  setScan(scan: Scan): void {
    this.lastScan = scan;
  }

  /**
   * Applies an answer file by file; says which files changed. `asked` is what the request asked
   * for: the whole list, or what changed `since` the generation the list has. An answer to a
   * request for the whole list replaces the list, whether or not it says `full`.
   */
  apply(answer: RuleListAnswer, asked: 'all' | 'since' = 'since'): ListChange {
    this.unreadWarned = false;
    if (answer.scan !== undefined) {
      this.lastScan = answer.scan;
    }
    const whole = answer.full || answer.generation === undefined || asked === 'all';
    if (!whole && this.current !== undefined && answer.generation < this.current) {
      // An answer that came late, after a newer one: it would put back what the newer one changed.
      return { files: [], full: false, stale: true };
    }
    if (answer.generation !== undefined) {
      this.current = answer.generation;
    }
    this.answered = true;
    // Entries with no file are not in any file: the whole list says how many there are, and an
    // answer of what changed only adds to what is known (a restart starts over).
    this.unattributedCount = whole
      ? answer.unattributed
      : Math.max(this.unattributedCount, answer.unattributed);
    return whole ? this.replaceAll(answer) : this.merge(answer);
  }

  /** Sets what a file holds, or drops it with none, and keeps the count of rules with a problem. */
  private setFile(uri: string, documents: ListedDocument[] | undefined): void {
    this.problemTotal -= this.problemsOf.get(uri) ?? 0;
    if (documents === undefined) {
      this.byFile.delete(uri);
      this.problemsOf.delete(uri);
      return;
    }
    let problems = 0;
    for (const document of documents) {
      problems +=
        document.unreadable === true ? 1 : document.rules.filter((rule) => rule.problem).length;
    }
    this.byFile.set(uri, documents);
    this.problemsOf.set(uri, problems);
    this.problemTotal += problems;
  }

  private grouped(documents: readonly ListedDocument[]): Map<string, ListedDocument[]> {
    const groups = new Map<string, ListedDocument[]>();
    for (const document of documents) {
      const list = groups.get(document.uri);
      if (list === undefined) {
        groups.set(document.uri, [document]);
      } else {
        list.push(document);
      }
    }
    return groups;
  }

  private replaceAll(answer: RuleListAnswer): ListChange {
    const groups = this.grouped(answer.documents);
    const files = new Set<string>([...this.byFile.keys(), ...groups.keys()]);
    for (const uri of [...this.byFile.keys()]) {
      this.setFile(uri, undefined);
    }
    for (const [uri, documents] of groups) {
      this.setFile(uri, documents);
    }
    this.order = [...groups.keys()];
    return { files: [...files], full: true, stale: false };
  }

  private merge(answer: RuleListAnswer): ListChange {
    const changed = new Set<string>();
    for (const uri of answer.removed) {
      if (this.byFile.has(uri)) {
        this.setFile(uri, undefined);
        this.order = this.order.filter((each) => each !== uri);
        changed.add(uri);
      }
    }
    for (const [uri, documents] of this.grouped(answer.documents)) {
      if (!this.byFile.has(uri)) {
        this.insert(uri, documents[0]);
      }
      this.setFile(uri, documents);
      changed.add(uri);
    }
    return { files: [...changed], full: false, stale: false };
  }

  /** Puts a new file in the order: before the first file of its folder whose path sorts after its own. */
  private insert(uri: string, first: ListedDocument | undefined): void {
    if (first === undefined) {
      return;
    }
    let lastOfFolder = -1;
    for (const [at, each] of this.order.entries()) {
      const other = this.byFile.get(each)?.[0];
      if (other === undefined || other.folder !== first.folder) {
        continue;
      }
      lastOfFolder = at;
      if (precedes(first, other)) {
        this.order.splice(at, 0, uri);
        return;
      }
    }
    // After the last file of its folder, or, with none, where the engine puts open files and new folders: last.
    this.order.splice(lastOfFolder === -1 ? this.order.length : lastOfFolder + 1, 0, uri);
  }
}

/** The commands the engine lists in `executeCommandProvider` of its `initialize` result; none when it lists none. */
export function parseListedCommands(initializeResult: unknown): string[] {
  const capabilities = isRecord(initializeResult) ? initializeResult['capabilities'] : undefined;
  const provider = isRecord(capabilities) ? capabilities['executeCommandProvider'] : undefined;
  const commands = isRecord(provider) ? provider['commands'] : undefined;
  return Array.isArray(commands)
    ? commands.filter((command): command is string => typeof command === 'string')
    : [];
}

/** Whether an engine's refusal of a command says it does not know the command, not that it failed on it. */
export function isUnknownCommandError(error: unknown): boolean {
  if (!isRecord(error)) {
    return false;
  }
  const message = typeof error['message'] === 'string' ? error['message'] : '';
  return (
    error['code'] === -32601 || /unknown command|method not found|no such command/i.test(message)
  );
}
