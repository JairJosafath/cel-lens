import { execFile } from 'node:child_process';
import { readFileSync, statSync } from 'node:fs';
import { promisify } from 'node:util';

import { describe, expect, it } from 'vitest';

interface Manifest {
  contributes: { walkthroughs: { steps: { id: string; media: { svg?: string } }[] }[] };
}

const MANIFEST = JSON.parse(readFileSync('package.json', 'utf8')) as Manifest;
const PICTURES = MANIFEST.contributes.walkthroughs.flatMap((walkthrough) =>
  walkthrough.steps.map((step) => step.media.svg ?? `images/walkthrough/${step.id}.svg`),
);
const SIZE_LIMIT = 20 * 1024;

/** The properties of SVG and CSS that take a colour. */
const COLOR_PROPERTIES = new Set([
  'color',
  'fill',
  'flood-color',
  'lighting-color',
  'stop-color',
  'stroke',
]);

/** CSS's named colours, `transparent` among them; `currentColor` is the theme's text colour. */
const NAMED_COLORS = new Set(
  (
    'aliceblue antiquewhite aqua aquamarine azure beige bisque black blanchedalmond blue ' +
    'blueviolet brown burlywood cadetblue chartreuse chocolate coral cornflowerblue cornsilk ' +
    'crimson cyan darkblue darkcyan darkgoldenrod darkgray darkgreen darkgrey darkkhaki ' +
    'darkmagenta darkolivegreen darkorange darkorchid darkred darksalmon darkseagreen ' +
    'darkslateblue darkslategray darkslategrey darkturquoise darkviolet deeppink deepskyblue ' +
    'dimgray dimgrey dodgerblue firebrick floralwhite forestgreen fuchsia gainsboro ghostwhite ' +
    'gold goldenrod gray green greenyellow grey honeydew hotpink indianred indigo ivory khaki ' +
    'lavender lavenderblush lawngreen lemonchiffon lightblue lightcoral lightcyan ' +
    'lightgoldenrodyellow lightgray lightgreen lightgrey lightpink lightsalmon lightseagreen ' +
    'lightskyblue lightslategray lightslategrey lightsteelblue lightyellow lime limegreen linen ' +
    'magenta maroon mediumaquamarine mediumblue mediumorchid mediumpurple mediumseagreen ' +
    'mediumslateblue mediumspringgreen mediumturquoise mediumvioletred midnightblue mintcream ' +
    'mistyrose moccasin navajowhite navy oldlace olive olivedrab orange orangered orchid ' +
    'palegoldenrod palegreen paleturquoise palevioletred papayawhip peachpuff peru pink plum ' +
    'powderblue purple rebeccapurple red rosybrown royalblue saddlebrown salmon sandybrown ' +
    'seagreen seashell sienna silver skyblue slateblue slategray slategrey snow springgreen ' +
    'steelblue tan teal thistle tomato turquoise violet wheat white whitesmoke yellow ' +
    'yellowgreen transparent'
  ).split(' '),
);

/** A theme variable, optionally falling back to another, or to `transparent` (an outline in no high-contrast theme). */
const THEME_VARIABLE = /^var\(--vscode-[A-Za-z0-9-]+(?:,\s*(?:transparent|(.+)))?\)$/;

/** Whether a colour value is a theme variable (with its fallbacks), `currentColor` or `none`. */
function isThemeColor(value: string): boolean {
  if (value === 'none' || value === 'currentColor') {
    return true;
  }
  const match = THEME_VARIABLE.exec(value);
  if (match === null) {
    return false;
  }
  const fallback = match[1];
  return fallback === undefined || isThemeColor(fallback.trim());
}

/** The literal colours in a value: hex, a colour function or a named colour, outside the theme variables' names. */
function literalColors(value: string): string[] {
  const bare = value
    .replace(/var\(--vscode-[A-Za-z0-9-]+,\s*transparent\)/g, ' ')
    .replace(/--vscode-[A-Za-z0-9-]+/g, ' ');
  return [
    ...(bare.match(/#[0-9a-f]{3,8}\b/gi) ?? []),
    ...(bare.match(/\b(?:rgba?|hsla?|hwb|lab|lch|oklab|oklch|color)\(/gi) ?? []),
    ...(bare.match(/[A-Za-z]+/g) ?? []).filter((word) => NAMED_COLORS.has(word.toLowerCase())),
  ];
}

interface Element {
  readonly name: string;
  readonly attributes: ReadonlyMap<string, string>;
  /** The text right inside it, such as a `<title>`'s or a `<style>`'s. */
  text: string;
  readonly parent: Element | undefined;
}

const TAG = /<(\/?)([A-Za-z][\w:-]*)((?:\s+[\w:-]+="[^"]*")*)\s*(\/?)>/y;
const ATTRIBUTE = /([\w:-]+)="([^"]*)"/g;

/** Reads an SVG's elements, failing on markup it cannot read or tags that do not close. */
function parseSvg(source: string): Element[] {
  const elements: Element[] = [];
  const open: Element[] = [];
  let at = 0;
  while (at < source.length) {
    const next = source.indexOf('<', at);
    const text = source.slice(at, next === -1 ? source.length : next);
    const current = open.at(-1);
    if (current === undefined) {
      if (text.trim() !== '') {
        throw new Error(`text outside the root: ${text.trim()}`);
      }
    } else {
      current.text += text;
    }
    if (next === -1) {
      break;
    }
    if (source.startsWith('<!--', next)) {
      const end = source.indexOf('-->', next);
      if (end === -1) {
        throw new Error('a comment does not end');
      }
      at = end + 3;
      continue;
    }
    if (source.startsWith('<?xml', next)) {
      at = source.indexOf('?>', next) + 2;
      continue;
    }
    TAG.lastIndex = next;
    const tag = TAG.exec(source);
    if (tag === null) {
      throw new Error(
        `cannot read the markup at ${String(next)}: ${source.slice(next, next + 40)}`,
      );
    }
    const [whole, closing = '', name = '', attributeText = '', selfClosing = ''] = tag;
    at = next + whole.length;
    if (closing === '/') {
      const closed = open.pop();
      if (closed?.name !== name) {
        throw new Error(`</${name}> closes <${closed?.name ?? 'nothing'}>`);
      }
      continue;
    }
    const element: Element = {
      name,
      attributes: new Map(
        [...attributeText.matchAll(ATTRIBUTE)].map((match) => [match[1] ?? '', match[2] ?? '']),
      ),
      text: '',
      parent: open.at(-1),
    };
    elements.push(element);
    if (selfClosing !== '/') {
      open.push(element);
    }
  }
  if (open.length > 0) {
    throw new Error(`<${open.map((element) => element.name).join('>, <')}> never closes`);
  }
  return elements;
}

/** The declarations of a `style` attribute or the rules of a `<style>`, as property and value. */
function declarations(css: string): [string, string][] {
  return css
    .replace(/\/\*[\s\S]*?\*\//g, '')
    .replace(/[^{}]*\{([^}]*)\}/g, '$1;')
    .split(';')
    .map((declaration) => declaration.trim())
    .filter((declaration) => declaration !== '')
    .map((declaration) => {
      const colon = declaration.indexOf(':');
      return [declaration.slice(0, colon).trim(), declaration.slice(colon + 1).trim()];
    });
}

/** What is wrong with the colours of an SVG: each literal colour, and each colour that is not a theme variable. */
function colorProblems(source: string): string[] {
  const problems: string[] = [];
  const check = (where: string, property: string, value: string): void => {
    for (const literal of literalColors(value)) {
      problems.push(`${where}: ${property} has the literal colour ${literal}`);
    }
    if (COLOR_PROPERTIES.has(property) && !isThemeColor(value)) {
      problems.push(`${where}: ${property} is ${value}, not a theme variable`);
    }
  };
  for (const element of parseSvg(source)) {
    for (const [name, value] of element.attributes) {
      if (name === 'style') {
        for (const [property, declared] of declarations(value)) {
          check(`<${element.name} style>`, property, declared);
        }
      } else {
        check(`<${element.name}>`, name, value);
      }
    }
    if (element.name === 'style') {
      for (const [property, declared] of declarations(element.text)) {
        check('<style>', property, declared);
      }
    }
  }
  return problems;
}

describe('colorProblems', () => {
  it.each([
    ['a theme variable', '<svg><rect fill="var(--vscode-editor-background)"/></svg>'],
    [
      'an outline in contrastBorder',
      '<svg><rect stroke="var(--vscode-contrastBorder, transparent)"/></svg>',
    ],
    [
      'a variable falling back to another',
      '<svg><style>.a { fill: var(--vscode-a, var(--vscode-b)); }</style></svg>',
    ],
    ['currentColor and none', '<svg><path fill="none" stroke="currentColor"/></svg>'],
    [
      'a variable whose name holds a colour',
      '<svg><rect fill="var(--vscode-terminal-ansiRed)"/></svg>',
    ],
    ['a colour word in text', '<svg><title>Plum and lavender</title></svg>'],
  ])('finds nothing wrong with %s', (_name, source) => {
    expect(colorProblems(source)).toEqual([]);
  });

  it.each([
    ['a hex attribute', '<svg><rect fill="#1e1e1e"/></svg>'],
    ['a short hex in a style attribute', '<svg><rect style="stroke: #fff"/></svg>'],
    ['rgb( in a style element', '<svg><style>.a { fill: rgb(30, 30, 30); }</style></svg>'],
    ['hsl( in an attribute', '<svg><rect fill="hsl(0 0% 12%)"/></svg>'],
    ['a named colour', '<svg><text fill="white">x</text></svg>'],
    ['a named colour in a style element', '<svg><style>.a { stroke: Red; }</style></svg>'],
    ['a named colour as a fallback', '<svg><rect fill="var(--vscode-a, black)"/></svg>'],
    ['transparent on its own', '<svg><rect fill="transparent"/></svg>'],
    ['a colour on a non-colour property', '<svg><rect filter="drop-shadow(0 0 2px #000)"/></svg>'],
  ])('fails on %s', (_name, source) => {
    expect(colorProblems(source)).not.toEqual([]);
  });

  it.each([
    ['an element that never closes', '<svg><g></svg>'],
    ['markup it cannot read', '<svg><rect fill=#fff/></svg>'],
  ])('throws on %s', (_name, source) => {
    expect(() => colorProblems(source)).toThrow();
  });
});

describe('the walkthrough’s pictures', () => {
  it('has one picture per step, in images/walkthrough/', () => {
    expect(PICTURES).toEqual([
      'images/walkthrough/openSample.svg',
      'images/walkthrough/fixProblem.svg',
      'images/walkthrough/pickVersion.svg',
      'images/walkthrough/chooseColors.svg',
    ]);
  });

  it.each(PICTURES)('%s takes every colour from the theme', (path) => {
    expect(colorProblems(readFileSync(path, 'utf8'))).toEqual([]);
  });

  it.each(PICTURES)('%s is an <svg> with a <title> that says what it shows', (path) => {
    const elements = parseSvg(readFileSync(path, 'utf8'));
    const [root] = elements;
    expect(root?.name).toBe('svg');
    expect(root?.attributes.get('xmlns')).toBe('http://www.w3.org/2000/svg');
    const title = elements.find((element) => element.name === 'title');
    expect(title?.parent).toBe(root);
    expect(title?.text.trim().length ?? 0).toBeGreaterThan(20);
  });

  it.each(PICTURES)(
    '%s has no <style> and no style attribute, which the Welcome page’s Content Security Policy blocks, so it colours with attributes',
    (path) => {
      const elements = parseSvg(readFileSync(path, 'utf8'));
      expect(elements.filter((element) => element.name === 'style')).toEqual([]);
      expect(elements.filter((element) => element.attributes.has('style'))).toEqual([]);
      expect(elements.filter((element) => element.attributes.has('fill')).length).toBeGreaterThan(
        0,
      );
    },
  );

  it.each(PICTURES)('%s is under 20 KB', (path) => {
    expect(statSync(path).size).toBeLessThan(SIZE_LIMIT);
  });

  it('ships in the .vsix: .vscodeignore takes images/walkthrough/*.svg, and vsce lists each picture', async () => {
    const ignore = readFileSync('.vscodeignore', 'utf8').split('\n');
    expect(ignore).toContain('!images/walkthrough/*.svg');

    const { stdout } = await promisify(execFile)('node_modules/.bin/vsce', [
      'ls',
      '--no-dependencies',
    ]);
    const packaged = stdout.split('\n').map((line) => line.trim());
    for (const path of PICTURES) {
      expect(packaged).toContain(path);
    }
  });
});
