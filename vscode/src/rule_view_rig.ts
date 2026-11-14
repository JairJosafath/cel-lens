// What the tests of the view are made with: a view over a fake engine, and answers to give it.
import { vi } from 'vitest';

import { LogChannel, contextKeys, settle } from '../test/vscode';
import { rawAnswer, rawDocument, rawRule } from './rule_list_samples';
import type { TreeNode } from './rule_tree';
import {
  CHANGE_PAUSE_MS,
  RuleView,
  type RuleViewOptions,
  type RunEngineCommand,
} from './rule_view';
import { RULE_VIEW_STATE_KEY } from './rule_view_state';

export const W = 'file:///workspace/repo';
export const INITIALIZE_RESULT = {
  capabilities: {
    experimental: {
      celLens: { workspaceScanMaxFiles: 20000, workspaceScanMaxFileBytes: 1048576 },
    },
  },
};

export type Answer = Record<string, unknown>;

/** A CRD file of `rules` rules in the workspace folder `repo`. */
export function crd(file: string, name: string, rules: number): Answer {
  const uri = `${W}/${file}`;
  const document = `CustomResourceDefinition ${name}`;
  return rawDocument(
    uri,
    Array.from({ length: rules }, (_, index) => rawRule(uri, document, index)),
    { name, folder: W, file },
  );
}

export interface Rig {
  readonly view: RuleView;
  readonly run: ReturnType<typeof vi.fn<RunEngineCommand>>;
  readonly log: LogChannel;
  /** The answers the engine gives, in order; the last is repeated. */
  answers: unknown[];
}

export function rig(
  answers: unknown[] = [rawAnswer([], { generation: 1 })],
  options: RuleViewOptions = {},
): Rig {
  const made: Rig = {
    answers,
    log: new LogChannel('cel-lens'),
    run: vi.fn<RunEngineCommand>(),
    view: undefined as never,
  };
  let asked = 0;
  made.run.mockImplementation(() => {
    const next = made.answers[Math.min(asked, made.answers.length - 1)];
    asked += 1;
    return next instanceof Error ? Promise.reject(next) : Promise.resolve(next);
  });
  return Object.assign(made, {
    view: new RuleView(made.run, made.log, { pauseMs: CHANGE_PAUSE_MS, ...options }),
  });
}

/** Starts the engine of a rig and waits for its first answer. */
export async function start(made: Rig): Promise<void> {
  made.view.engineStarting();
  made.view.engineStarted(INITIALIZE_RESULT);
  await settle();
}

/** The value, which a test knows is there. */
export function there<T>(value: T | undefined): T {
  if (value === undefined) {
    throw new Error('the row is not there');
  }
  return value;
}

export function labels(nodes: readonly TreeNode[]): string[] {
  return nodes.map((node) => node.label);
}

export function state(): unknown {
  return contextKeys.get(RULE_VIEW_STATE_KEY);
}
