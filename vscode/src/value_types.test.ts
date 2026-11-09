import { describe, expect, it } from 'vitest';

import {
  INT_OR_STRING_LINES,
  WRITE_STRUCT_LINE,
  isCurrentChoice,
  editTypeTitle,
  engineFailure,
  giveTypeTitle,
  inCrd,
  isChangeAnswer,
  lineValues,
  parseTypeChoices,
  parseTypeProblem,
  parseTypeTarget,
  problemText,
  typeLines,
  typedType,
  valueLines,
  withKind,
  withType,
  type PickLine,
  type TypeChoice,
  type TypeChoices,
  type TypeTarget,
} from './value_types';

/** The argument of the Give it a type part of a CRD's value, as the engine sends it. */
const CRD_VALUE = {
  cellens: 'file:///ws/.cellens',
  file: 'deploy/crd.yaml',
  document: 'CustomResourceDefinition widgets.example.com',
  version: 'v1',
  path: 'spec.maxSurge',
  value: 'self.spec.maxSurge',
};

/** The argument of the Edit part of a policy's value an entry gives a kind. */
const POLICY_VALUE = {
  cellens: 'file:///ws/.cellens',
  file: 'policies/limits.yaml',
  document: 'ValidatingAdmissionPolicy limits',
  variable: 'params',
  path: 'spec.config',
  value: 'params.spec.config',
  kind: 'apps/v1 Deployment',
};

function target(argument: Record<string, unknown>): TypeTarget {
  const parsed = parseTypeTarget(argument);
  if (parsed === undefined) {
    throw new Error('the argument names no value');
  }
  return parsed;
}

const CHOICES: TypeChoices = {
  kubernetesVersion: '1.37',
  builtIn: [
    {
      apiVersion: 'apps/v1',
      kind: 'Deployment',
      description: 'Deployment enables declarative updates for Pods and ReplicaSets.',
      stability: 'ga',
    },
    {
      apiVersion: 'storage.k8s.io/v1alpha1',
      kind: 'VolumeAttributesClass',
      description: '',
      stability: 'alpha',
    },
  ],
  crds: [
    { apiVersion: 'example.com/v1', kind: 'Widget', description: 'A widget.', stability: 'ga' },
  ],
};

describe('parseTypeTarget', () => {
  it('reads the value of a CRD, keeping the argument as the engine sent it', () => {
    expect(parseTypeTarget(CRD_VALUE)).toEqual({
      argument: CRD_VALUE,
      value: 'self.spec.maxSurge',
      document: 'CustomResourceDefinition widgets.example.com',
      version: 'v1',
    });
  });

  it("reads a policy's value and the kind an entry gives it", () => {
    expect(parseTypeTarget(POLICY_VALUE)).toMatchObject({
      value: 'params.spec.config',
      document: 'ValidatingAdmissionPolicy limits',
      kind: 'apps/v1 Deployment',
    });
  });

  it('names a value without its CEL "this value"', () => {
    expect(parseTypeTarget({ ...CRD_VALUE, value: undefined })?.value).toBe('this value');
  });

  it.each([
    ['no cellens', { ...CRD_VALUE, cellens: '' }],
    ['no file', { ...CRD_VALUE, file: undefined }],
    ['a document that is no string', { ...CRD_VALUE, document: 3 }],
    ['a list', [CRD_VALUE]],
    ['null', null],
    ['a string', 'self.spec.maxSurge'],
  ])('names no value with %s', (_name, argument) => {
    expect(parseTypeTarget(argument)).toBeUndefined();
  });
});

describe('inCrd', () => {
  it.each([
    [CRD_VALUE, true],
    [POLICY_VALUE, false],
    [{ ...CRD_VALUE, document: 'CustomResourceDefinitionList x' }, false],
  ])('%j is in a CRD: %s', (argument, expected) => {
    expect(inCrd(target(argument))).toBe(expected);
  });
});

describe('withType and withKind', () => {
  it('give the engine the type and drop the kind the value has', () => {
    expect(withType(target(POLICY_VALUE), '{name: string}')).toEqual({
      ...Object.fromEntries(Object.entries(POLICY_VALUE).filter(([key]) => key !== 'kind')),
      type: '{name: string}',
    });
  });

  it('give the engine the kind and drop the type the value has', () => {
    const typed = target({ ...CRD_VALUE, type: 'int' });
    expect(withKind(typed, 'v1 ConfigMap')).toEqual({ ...CRD_VALUE, kind: 'v1 ConfigMap' });
  });
});

describe('titles', () => {
  it.each([
    [giveTypeTitle, 'Give self.spec.config a type'],
    [editTypeTitle, 'Edit the type of self.spec.config'],
  ])('%o names the value', (title, expected) => {
    expect(title('self.spec.config')).toBe(expected);
  });
});

describe('parseTypeChoices', () => {
  it('reads the built-in kinds, the CRDs and the note, as the engine documents them', () => {
    expect(
      parseTypeChoices({
        kubernetesVersion: '1.37',
        builtIn: [
          {
            apiVersion: 'apps/v1',
            group: 'apps',
            version: 'v1',
            kind: 'Deployment',
            description: 'Deployment enables declarative updates.',
            stability: 'ga',
          },
          { apiVersion: 'v1beta1', kind: 'Thing', stability: 'beta' },
          { apiVersion: 'v1', kind: '' },
          'Pod',
        ],
        crds: [],
        crdsNote: 'only with crd-typed-policies on',
      }),
    ).toEqual({
      kubernetesVersion: '1.37',
      builtIn: [
        {
          apiVersion: 'apps/v1',
          kind: 'Deployment',
          description: 'Deployment enables declarative updates.',
          stability: 'ga',
        },
        { apiVersion: 'v1beta1', kind: 'Thing', description: '', stability: 'beta' },
      ],
      crds: [],
      crdsNote: 'only with crd-typed-policies on',
    });
  });

  it.each([null, [], { crds: [] }, { builtIn: 'Deployment' }])(
    'reads nothing from %j',
    (answer) => {
      expect(parseTypeChoices(answer)).toBeUndefined();
    },
  );
});

describe('typeLines', () => {
  it('starts with Write a struct… and its pencil, then the built-in kinds, then the CRDs, each group under a separator', () => {
    expect(typeLines(CHOICES)).toEqual([
      WRITE_STRUCT_LINE,
      { separator: true, label: 'Built-in kinds · Kubernetes 1.37' },
      {
        label: '$(symbol-class) Deployment',
        description: 'apps/v1',
        detail: 'Deployment enables declarative updates for Pods and ReplicaSets.',
        value: { action: 'kind', kind: 'apps/v1 Deployment' },
      },
      {
        label: '$(symbol-class) VolumeAttributesClass',
        description: 'storage.k8s.io/v1alpha1 · alpha',
        value: { action: 'kind', kind: 'storage.k8s.io/v1alpha1 VolumeAttributesClass' },
      },
      { separator: true, label: 'Workspace CRDs' },
      {
        label: '$(file-code) Widget',
        description: 'example.com/v1',
        detail: 'A widget.',
        value: { action: 'kind', kind: 'example.com/v1 Widget' },
      },
    ]);
    expect(WRITE_STRUCT_LINE.label).toBe('$(edit) Write a struct…');
  });

  it('says why no CRD is listed, in a line that opens the setting', () => {
    const lines = typeLines({ ...CHOICES, crds: [], crdsNote: 'only with crd-typed-policies on' });

    expect(lines.slice(-2)).toEqual([
      { separator: true, label: 'Workspace CRDs' },
      {
        label: '$(info) Workspace CRDs are not listed',
        description: 'pick to open the setting',
        detail: 'only with crd-typed-policies on',
        value: { action: 'openCrdSetting' },
      },
    ]);
  });

  it('has no CRD group when the workspace defines none, and no version in the separator when none is known', () => {
    expect(typeLines({ ...CHOICES, kubernetesVersion: '', crds: [] }).map((l) => l.label)).toEqual([
      '$(edit) Write a struct…',
      'Built-in kinds',
      '$(symbol-class) Deployment',
      '$(symbol-class) VolumeAttributesClass',
    ]);
  });

  it('is only Write a struct… with no kind at all', () => {
    expect(typeLines({ kubernetesVersion: '1.37', builtIn: [], crds: [] })).toEqual([
      WRITE_STRUCT_LINE,
    ]);
  });
});

describe('typedType', () => {
  it.each([
    ['{name: string}', '{name: string}'],
    ['  list({port: int}) ', 'list({port: int})'],
    ['map(string, int)', 'map(string, int)'],
    ['string', 'string'],
    ['deploy', undefined],
    ['Deployment', undefined],
    ['', undefined],
  ])('reads %j typed in the filter as the type %j', (filter, expected) => {
    expect(typedType(filter)).toBe(expected);
  });

  it('keeps Write a struct… in the list whatever is typed, so Enter on a struct typed there picks it', () => {
    expect(WRITE_STRUCT_LINE.alwaysShow).toBe(true);
  });
});

describe('INT_OR_STRING_LINES', () => {
  it('offers int and string, the two types an x-kubernetes-int-or-string value takes', () => {
    expect(INT_OR_STRING_LINES.map((line) => line.label)).toEqual([
      '$(symbol-numeric) int',
      '$(symbol-string) string',
    ]);
  });
});

describe('isCurrentChoice', () => {
  /** The label of the first line that is the value's type or kind now. */
  function current(lines: readonly PickLine<TypeChoice>[], argument: Record<string, unknown>) {
    const isCurrent = isCurrentChoice(target(argument));
    return lines.find((line) => line.separator !== true && isCurrent(line.value))?.label;
  }

  it.each([
    ['the kind it has', POLICY_VALUE, '$(symbol-class) Deployment'],
    ['nothing for a kind not listed', { ...POLICY_VALUE, kind: 'v1 Nothing' }, undefined],
    ['nothing for dyn', CRD_VALUE, undefined],
  ])('finds %s', (_name, argument, label) => {
    expect(current(typeLines(CHOICES), argument)).toBe(label);
  });

  it('finds the int of a CRD value typed int', () => {
    expect(current(INT_OR_STRING_LINES, { ...CRD_VALUE, type: 'int' })).toBe(
      '$(symbol-numeric) int',
    );
  });
});

describe('parseTypeProblem and problemText', () => {
  it.each([
    [null, null],
    [
      { message: 'unknown type "strin": use string, int, ...', column: 8 },
      { message: 'unknown type "strin": use string, int, ...', column: 8 },
    ],
    [{ message: 'give it int or string', column: 0 }, { message: 'give it int or string' }],
    [{ message: 'give it int or string' }, { message: 'give it int or string' }],
    [{ column: 3 }, undefined],
    [undefined, undefined],
    ['fine', undefined],
  ])('reads %j as %j', (answer, expected) => {
    expect(parseTypeProblem(answer)).toEqual(expected);
  });

  it.each([
    [{ message: 'unknown type "strin"', column: 8 }, 'unknown type "strin" (character 8)'],
    [{ message: 'give it int or string' }, 'give it int or string'],
  ])('shows %j as %j', (problem, expected) => {
    expect(problemText(problem)).toBe(expected);
  });
});

describe('isChangeAnswer', () => {
  it.each([
    [{ cellens: 'file:///ws/.cellens', change: 'added' }, true],
    [{ cellens: 'file:///ws/.cellens' }, false],
    [null, false],
  ])('%j is an answer of setType or removeType: %s', (answer, expected) => {
    expect(isChangeAnswer(answer)).toBe(expected);
  });
});

describe('engineFailure', () => {
  const waiting =
    'an edit of .cellens is waiting for the editor: run the command again once it is applied';

  it.each([
    [
      { code: -32803, message: waiting },
      { kind: 'refused', reason: waiting },
    ],
    [
      { code: -32602, message: 'cel-lens.setType: give type or kind, not both.' },
      { kind: 'refused', reason: 'give type or kind, not both' },
    ],
    [{ code: -32800, message: 'Request got cancelled' }, { kind: 'cancelled' }],
    [
      { code: -32603, message: 'internal error' },
      { kind: 'other', reason: 'internal error (error -32603)' },
    ],
    [
      new Error('the cel-lens engine is not running'),
      { kind: 'other', reason: 'the cel-lens engine is not running' },
    ],
    [
      { code: -32803, message: '' },
      { kind: 'other', reason: '[object Object] (error -32803)' },
    ],
    ['a string', { kind: 'other', reason: 'a string' }],
  ])('reads %j as %j', (error, expected) => {
    expect(engineFailure(error)).toEqual(expected);
  });
});

describe('lineValues', () => {
  const range = (line: number): unknown => ({
    start: { line, character: 4 },
    end: { line, character: 4 },
  });
  const typed = { ...CRD_VALUE, path: 'spec.config', value: 'self.spec.config', type: 'int' };
  const port = { ...CRD_VALUE, path: 'spec.port', value: 'self.spec.port' };

  it('takes the values of the Give it a type and Edit parts, also those +N more holds, each once, in the order of the lines', () => {
    const lenses = [
      {
        range: range(14),
        command: {
          title: 'self: Widget.spec · compiles',
          command: 'cel-lens.showRule',
          arguments: [{}],
        },
      },
      {
        range: range(14),
        command: {
          title: 'self.spec.maxSurge: dyn · Give it a type',
          command: 'cel-lens.giveType',
          arguments: [CRD_VALUE],
        },
      },
      {
        range: range(14),
        command: {
          title: 'self.spec.config: int · Edit',
          command: 'cel-lens.editType',
          arguments: [typed],
        },
      },
      {
        range: range(14),
        command: { title: 'Remove', command: 'cel-lens.removeType', arguments: [typed] },
      },
      {
        range: range(14),
        command: {
          title: '+1 more',
          command: 'cel-lens.listValues',
          arguments: [
            {
              line: 15,
              parts: [
                {
                  title: 'self.spec.port: dyn · Give it a type',
                  command: 'cel-lens.giveType',
                  arguments: [port],
                },
              ],
            },
          ],
        },
      },
      {
        range: range(20),
        command: {
          title: 'self.spec.maxSurge: dyn · Give it a type',
          command: 'cel-lens.giveType',
          arguments: [CRD_VALUE],
        },
      },
    ];

    expect(
      lineValues(lenses).map(({ target: { value }, command, line }) => ({ value, command, line })),
    ).toEqual([
      { value: 'self.spec.maxSurge', command: 'cel-lens.giveType', line: 15 },
      { value: 'self.spec.config', command: 'cel-lens.editType', line: 15 },
      { value: 'self.spec.port', command: 'cel-lens.giveType', line: 15 },
    ]);
  });

  it('reads the parts of a +N more lens the client has not given its line', () => {
    const lenses = [
      {
        range: range(3),
        command: {
          title: '+1 more',
          command: 'cel-lens.listValues',
          arguments: [
            {
              title: 'self.spec.port: dyn · Give it a type',
              command: 'cel-lens.giveType',
              arguments: [port],
            },
          ],
        },
      },
    ];

    expect(lineValues(lenses).map((value) => value.target.value)).toEqual(['self.spec.port']);
  });

  it.each([
    ['no lenses', undefined],
    ['a lens with no command', [{ range: range(1) }]],
    [
      'a lens with no range',
      [{ command: { title: 't', command: 'cel-lens.giveType', arguments: [CRD_VALUE] } }],
    ],
    [
      'a part with no value',
      [{ range: range(1), command: { title: 't', command: 'cel-lens.giveType', arguments: [{}] } }],
    ],
  ])('finds nothing in %s', (_name, lenses) => {
    expect(lineValues(lenses)).toEqual([]);
  });
});

describe('valueLines', () => {
  it('shows each value with its type now, dyn when it has none, and its document and line', () => {
    const policy = target({ ...POLICY_VALUE });
    expect(
      valueLines([
        { target: target(CRD_VALUE), command: 'cel-lens.giveType', line: 15 },
        { target: policy, command: 'cel-lens.editType', line: 30 },
      ]).map(({ label, description, detail }) => ({ label, description, detail })),
    ).toEqual([
      {
        label: 'self.spec.maxSurge',
        description: 'dyn',
        detail: 'CustomResourceDefinition widgets.example.com v1 · line 15',
      },
      {
        label: 'params.spec.config',
        description: 'apps/v1 Deployment',
        detail: 'ValidatingAdmissionPolicy limits · line 30',
      },
    ]);
  });
});
