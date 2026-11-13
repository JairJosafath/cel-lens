import {
  COMPUTING_LINE,
  ENGINE_EXAMPLE_RESULTS,
  NO_RESULTS_LINE,
  RULE_CHANGED_LINE,
  RESULTS_PLACEHOLDER,
  SHOW_EXAMPLE_RESULTS_COMMAND,
  failedLine,
  parseRuleResults,
  resultLines,
  resultsTitle,
  type ResultPick,
} from './example_results';
import { parseRuleLocation, type RuleLocation } from './rule_parts';
import { BACK, type EngineCommand, type FlowLog, type PickRequest, type Picked } from './type_flow';
import { engineFailure, type PickLine } from './value_types';

/** What the flow shows a person; the extension shows it with VS Code. */
export interface ResultsUi {
  /** Shows the quick pick; the line picked, `BACK` for a Back button, or `undefined` on Escape. */
  pick(request: PickRequest<ResultPick>): Promise<Picked<ResultPick> | typeof BACK | undefined>;
  /** Whether example results are on for the file at `uri`. */
  isOn(uri: string): boolean;
  /** Says the results are off for the file, and offers a button that turns them on. */
  offerTurnOn(): Promise<void>;
  /**
   * Opens the example at `uri` beside the rule's editor, or in the editor group that already shows
   * it, with the cursor at the start of `line` (from 1).
   */
  openExample(uri: string, line: number): Promise<void>;
  /** Shows an error once; `offerLogs` adds the button that opens the logs. */
  showError(message: string, offerLogs: boolean): Promise<void>;
  showLogs(): void;
}

/**
 * `cel-lens.showExampleResults`, the part of the line above a CRD's rule: a picker of what the
 * rule does to each example of the workspace, the wrong verdicts first, each with its file and
 * line and the API server's message. The picker opens at once, saying the results are being
 * computed, and fills in when the engine answers; Escape cancels the engine's work. Picking an
 * example opens it at its line beside the rule.
 */
export class ExampleResultsFlow {
  constructor(
    private readonly engine: EngineCommand,
    private readonly ui: ResultsUi,
    private readonly log: FlowLog,
  ) {}

  /** Runs the command with the rule's location, as the engine gives it to the part. */
  async show(argument: unknown): Promise<void> {
    const rule = parseRuleLocation(argument);
    if (rule === undefined) {
      this.log.warn(
        `${SHOW_EXAMPLE_RESULTS_COMMAND} was given no rule location; the engine may not match this extension.`,
      );
      return;
    }
    if (!this.ui.isOn(rule.uri)) {
      await this.ui.offerTurnOn();
      return;
    }
    const computing = new AbortController();
    try {
      const picked = await this.ui.pick({
        title: resultsTitle(rule.range.start.line + 1),
        placeholder: RESULTS_PLACEHOLDER,
        lines: this.lines(rule, computing.signal),
        first: [COMPUTING_LINE],
      });
      if (picked === undefined || picked === BACK) {
        return;
      }
      await this.act(picked.value);
    } finally {
      computing.abort();
    }
  }

  /** The lines of the picker: each example, or one line that says why there is none. Never rejects. */
  private async lines(rule: RuleLocation, signal: AbortSignal): Promise<PickLine<ResultPick>[]> {
    let answer: unknown;
    try {
      answer = await this.engine(
        ENGINE_EXAMPLE_RESULTS,
        { uri: rule.uri, range: rule.range },
        signal,
      );
    } catch (error) {
      return this.failure(error);
    }
    const results = parseRuleResults(answer, rule.range.start);
    if (results === undefined) {
      this.log.warn(
        `${ENGINE_EXAMPLE_RESULTS} gave no rules; the engine may not match this extension.`,
      );
      return [failedLine('The engine answered in a way this extension does not read', true)];
    }
    if (results === 'changed') {
      return [RULE_CHANGED_LINE];
    }
    return results.length === 0 ? [NO_RESULTS_LINE] : resultLines(results);
  }

  /** The line that says what went wrong: the engine's reason when it refused, else a way to the logs. */
  private failure(error: unknown): PickLine<ResultPick>[] {
    const failure = engineFailure(error);
    switch (failure.kind) {
      case 'cancelled':
        return [];
      case 'refused':
        this.log.warn(`Could not get the example results: ${failure.reason}`);
        return [failedLine(failure.reason, false)];
      case 'other':
        this.log.error(`Could not get the example results: ${failure.reason}`);
        return [failedLine('Show the logs for the reason', true)];
    }
  }

  private async act(pick: ResultPick): Promise<void> {
    switch (pick.action) {
      case 'open':
        try {
          await this.ui.openExample(pick.uri, pick.line);
        } catch (error) {
          this.log.error(`Could not open ${pick.uri}: ${String(error)}`);
          await this.ui.showError(
            `cel-lens could not open ${pick.file}. Show the logs for the reason.`,
            true,
          );
        }
        return;
      case 'showLogs':
        this.ui.showLogs();
        return;
      case 'none':
        return;
    }
  }
}
