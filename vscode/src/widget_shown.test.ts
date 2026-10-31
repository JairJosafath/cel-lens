import { runInNewContext } from 'node:vm';

import { describe, expect, it } from 'vitest';

import { widgetShown, type WidgetElement, type WidgetStyle } from './widget_shown.ts';

const SHOWN: WidgetStyle = { display: 'block', visibility: 'visible', opacity: '1' };

function widget(style: Partial<WidgetStyle>, playStates: readonly string[]): WidgetElement {
  const element: WidgetElement = {
    ownerDocument: {
      defaultView: { getComputedStyle: () => ({ ...SHOWN, ...style }) },
    },
    getAnimations: () => playStates.map((playState) => ({ playState })),
  };
  return element;
}

describe('widgetShown', () => {
  it.each([
    {
      name: 'true once opacity is 1 and every animation is finished',
      style: {},
      states: ['finished'],
      shown: true,
    },
    { name: 'true with no animation at all', style: {}, states: [], shown: true },
    {
      name: 'false while the opacity is below 1',
      style: { opacity: '0.4' },
      states: [],
      shown: false,
    },
    {
      name: 'false while an animation is running',
      style: {},
      states: ['finished', 'running'],
      shown: false,
    },
    { name: 'false for display none', style: { display: 'none' }, states: [], shown: false },
    {
      name: 'false for visibility hidden',
      style: { visibility: 'hidden' },
      states: [],
      shown: false,
    },
  ])('is $name', ({ style, states, shown }) => {
    expect(widgetShown(widget(style, states))).toBe(shown);
  });

  it('is false for an element without a window', () => {
    const detached: WidgetElement = {
      ownerDocument: { defaultView: null },
      getAnimations: () => [],
    };
    expect(widgetShown(detached)).toBe(false);
  });

  it('serialises standalone with toString() and runs with no import or free variable', () => {
    const evaluated: unknown = runInNewContext(`(${widgetShown.toString()})`);
    if (typeof evaluated !== 'function') {
      throw new Error('the serialised source did not evaluate to a function');
    }
    const standalone = evaluated as typeof widgetShown;
    expect(standalone(widget({}, ['finished']))).toBe(true);
    expect(standalone(widget({ opacity: '0.5' }, []))).toBe(false);
  });
});
