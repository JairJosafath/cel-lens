import { describe, expect, it } from 'vitest';

import { isLooking, ruleViewState, type ViewFacts } from './rule_view_state';

const SETTLED: ViewFacts = {
  engine: 'running',
  unsupported: false,
  failed: false,
  folders: 1,
  answered: true,
  scanning: false,
  problemsOnly: false,
  listed: true,
  rows: true,
};

function facts(over: Partial<ViewFacts>): ViewFacts {
  return { ...SETTLED, ...over };
}

describe('the state of the view', () => {
  it('is the tree whenever there is a row, whatever else is so', () => {
    expect(ruleViewState(SETTLED)).toBe('tree');
    expect(ruleViewState(facts({ engine: 'down', folders: 0, answered: false }))).toBe('tree');
  });

  it('says no folder is open when there is no folder and no row', () => {
    expect(ruleViewState(facts({ rows: false, folders: 0 }))).toBe('noFolder');
  });

  it('says the engine is not running when it is not, even with no folder', () => {
    expect(ruleViewState(facts({ rows: false, engine: 'down' }))).toBe('engineDown');
    expect(ruleViewState(facts({ rows: false, engine: 'down', folders: 0 }))).toBe('engineDown');
  });

  it('says the engine cannot list rules when it refused the command', () => {
    expect(ruleViewState(facts({ rows: false, unsupported: true, answered: false }))).toBe(
      'unsupported',
    );
  });

  it.each([
    ['the engine is starting', { engine: 'starting' as const }],
    ['the engine reports a scan under way', { scanning: true }],
    ['no answer came yet', { answered: false }],
  ])('says it is looking while %s', (_why, over) => {
    expect(ruleViewState(facts({ rows: false, ...over }))).toBe('scanning');
  });

  it('says nothing was found once an answer came, the scan is done and there is no row', () => {
    expect(ruleViewState(facts({ rows: false }))).toBe('empty');
  });

  it('says no rule has a problem when the filter is on, the list has rules and none shows', () => {
    expect(ruleViewState(facts({ rows: false, problemsOnly: true }))).toBe('noProblems');
  });

  it('says nothing was found, not that no rule has a problem, when the filter is on and the list is empty', () => {
    expect(ruleViewState(facts({ rows: false, problemsOnly: true, listed: false }))).toBe('empty');
  });

  it('says no rule has a problem only with the filter on', () => {
    expect(ruleViewState(facts({ rows: false, problemsOnly: false }))).toBe('empty');
  });

  it('says the engine is down before it says no rule has a problem', () => {
    expect(ruleViewState(facts({ rows: false, problemsOnly: true, engine: 'down' }))).toBe(
      'engineDown',
    );
  });

  it('puts no folder before looking: with none open there is nothing to look in', () => {
    expect(ruleViewState(facts({ rows: false, folders: 0, answered: false }))).toBe('noFolder');
  });
});

describe('the progress bar', () => {
  it.each([
    ['the engine starts', { engine: 'starting' as const }, true],
    ['the engine reports a scan under way', { scanning: true }, true],
    ['the first answer is awaited, with a folder open', { answered: false }, true],
    ['the first answer is awaited, with no folder', { answered: false, folders: 0 }, false],
    ['the engine starts, with no folder', { engine: 'starting' as const, folders: 0 }, false],
    ['the engine is down', { engine: 'down' as const, answered: false }, false],
    ['the engine refused the command', { unsupported: true, answered: false }, false],
    ['the last ask failed and nothing came', { failed: true, answered: false }, false],
    ['the scan is done', {}, false],
  ])('is %s: %s', (_why, over, expected) => {
    expect(isLooking(facts(over))).toBe(expected);
  });
});
