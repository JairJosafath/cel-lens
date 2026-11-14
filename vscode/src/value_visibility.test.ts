import { describe, expect, it } from 'vitest';

import { withoutValueParts } from './value_visibility';

/** A lens of the line `line`, running `command`; the text names it in a failure. */
function lens(line: number, command: string | undefined, text: string) {
  return {
    text,
    range: { start: { line } },
    ...(command !== undefined && { command: { command } }),
  };
}

function texts(lenses: ReturnType<typeof lens>[] | null | undefined): string[] | null | undefined {
  return lenses?.map((each) => each.text);
}

describe('withoutValueParts', () => {
  it('keeps the state of a line and drops its two values and +N more', () => {
    const lenses = [
      lens(4, 'cel-lens.showRule', 'self: Widget.spec · compiles'),
      lens(4, 'cel-lens.giveType', 'params: dyn · Give it a type'),
      lens(4, 'cel-lens.editType', 'variables.limit: int · Edit'),
      lens(4, 'cel-lens.listValues', '+2 more'),
    ];

    expect(texts(withoutValueParts(lenses))).toEqual(['self: Widget.spec · compiles']);
  });

  it('drops an inferred value part, which runs cel-lens.showRule like the state does', () => {
    const lenses = [
      lens(7, 'cel-lens.showRule', 'object: dyn · compiles'),
      lens(7, 'cel-lens.showRule', 'params: Params'),
    ];

    expect(texts(withoutValueParts(lenses))).toEqual(['object: dyn · compiles']);
  });

  it('keeps the example part of a line, after its values', () => {
    const lenses = [
      lens(2, 'cel-lens.showRule', 'self: Widget.spec · compiles'),
      lens(2, 'cel-lens.giveType', 'params: dyn · Give it a type'),
      lens(2, 'cel-lens.showExampleResults', '3 examples pass'),
    ];

    expect(texts(withoutValueParts(lenses))).toEqual([
      'self: Widget.spec · compiles',
      '3 examples pass',
    ]);
  });

  it('does each line of an answer by itself', () => {
    const lenses = [
      lens(1, 'cel-lens.showRule', 'one'),
      lens(1, 'cel-lens.giveType', 'one value'),
      lens(5, 'cel-lens.showRule', 'two'),
      lens(5, 'cel-lens.editType', 'two value'),
      lens(5, 'cel-lens.showExampleResults', 'two examples'),
    ];

    expect(texts(withoutValueParts(lenses))).toEqual(['one', 'two', 'two examples']);
  });

  it('keeps the first lens of a line whatever it runs', () => {
    const lenses = [lens(3, undefined, 'no command'), lens(3, 'cel-lens.giveType', 'value')];

    expect(texts(withoutValueParts(lenses))).toEqual(['no command']);
  });

  it.each([[null], [undefined], [[]]])('leaves %j as it is', (answer) => {
    expect(withoutValueParts(answer)).toEqual(answer);
  });
});
