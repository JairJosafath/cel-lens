import { describe, expect, it } from 'vitest';

import {
  GO_TYPES_FAILED_LINE,
  OUTDATED_ENGINE_MESSAGE,
  INT_OR_STRING_LINES,
  NO_GO_TYPES_LINE,
  PASTE_EXAMPLE_LINE,
  WRITE_SCHEMA_LINE,
  WRITE_SHORT_TYPE_LINE,
  editTypeTitle,
  engineFailure,
  giveTypeTitle,
  firstLines,
  goReference,
  goTypeLine,
  goTypeLines,
  goTypesIn,
  hasEntry,
  inCrd,
  isAtOrBelow,
  isChangeAnswer,
  isCurrentChoice,
  lineValues,
  parseEntryPlace,
  parseTypeChoices,
  parseTypeTarget,
  sameGo,
  snippetRefusal,
  typeLines,
  valueLines,
  withGo,
  withKind,
  withSchema,
  withSnippet,
  withType,
  writeLine,
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
  goTypes: [
    { folder: 'file:///ws/api/v1', name: 'QuotaSpec', doc: 'QuotaSpec is the quota of a team.' },
    { folder: 'file:///ws/internal/limits', name: 'Limit', doc: '' },
  ],
  examples: [
    {
      uri: 'file:///ws/config/samples/limit.yaml',
      file: 'config/samples/limit.yaml',
      line: 1,
      apiVersion: 'rules.example.com/v1',
      kind: 'ReplicaLimit',
    },
    {
      uri: 'file:///ws/config/samples/limit.yaml',
      file: 'config/samples/limit.yaml',
      line: 12,
      apiVersion: 'rules.example.com/v1',
      kind: 'ReplicaLimit',
    },
  ],
};

describe('parseTypeTarget', () => {
  it('reads the value of a CRD, keeping the argument as the engine sent it', () => {
    expect(parseTypeTarget(CRD_VALUE)).toEqual({
      argument: CRD_VALUE,
      cellens: 'file:///ws/.cellens',
      value: 'self.spec.maxSurge',
      document: 'CustomResourceDefinition widgets.example.com',
      version: 'v1',
      path: 'spec.maxSurge',
    });
  });

  it("reads a policy's value and the kind an entry gives it", () => {
    expect(parseTypeTarget(POLICY_VALUE)).toMatchObject({
      value: 'params.spec.config',
      document: 'ValidatingAdmissionPolicy limits',
      kind: 'apps/v1 Deployment',
    });
  });

  it.each([
    ['a Go type', { go: './api/v1#QuotaSpec' }, { go: './api/v1#QuotaSpec' }],
    ['a schema, which is a JSON object', { schema: { type: 'object' } }, { schema: true }],
    ['no schema for one that is no object', { schema: 'type: object' }, {}],
  ])("reads %s an entry gives a policy's value", (_name, entry, expected) => {
    const parsed = parseTypeTarget({ ...POLICY_VALUE, kind: undefined, ...entry });
    expect({ go: parsed?.go, schema: parsed?.schema }).toEqual({
      go: undefined,
      schema: undefined,
      ...expected,
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

describe('the arguments of setType', () => {
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

  it.each([
    ['a Go type', { go: './api/v1#Old' }],
    ['a schema', { schema: { type: 'object' } }],
    ['a kind', { kind: 'v1 ConfigMap' }],
    ['a type', { type: 'int' }],
  ])('give the engine one Go type, or one snippet, and drop %s the value has', (_name, entry) => {
    const typed = target({ ...CRD_VALUE, ...entry });

    expect(withGo(typed, './api/v1#QuotaSpec')).toEqual({ ...CRD_VALUE, go: './api/v1#QuotaSpec' });
    expect(withSchema(typed, { type: 'integer' })).toEqual({
      ...CRD_VALUE,
      schema: { type: 'integer' },
    });
    expect(withSnippet(typed, 'schema')).toEqual({ ...CRD_VALUE, snippet: 'schema' });
    expect(withSnippet(typed, 'type')).toEqual({ ...CRD_VALUE, snippet: 'type' });
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
        goTypes: [
          { folder: 'file:///repo/api/v1', name: 'QuotaSpec', doc: 'The quota of a team.' },
          { folder: 'file:///repo/api/v1', name: 'Limit' },
          { folder: 'file:///repo/api/v1', name: '' },
          'Owner',
        ],
        goTypesNote: 'the workspace has more than 1000 types: the first 1000 are listed',
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
      goTypes: [
        { folder: 'file:///repo/api/v1', name: 'QuotaSpec', doc: 'The quota of a team.' },
        { folder: 'file:///repo/api/v1', name: 'Limit', doc: '' },
      ],
      goTypesNote: 'the workspace has more than 1000 types: the first 1000 are listed',
      examples: [],
      cannotInfer: true,
    });
  });

  it("reads an answer with no examples key as an engine that cannot work out a type from an example, and [] as none (#9's review)", () => {
    expect(parseTypeChoices({ builtIn: [], goTypes: [] })?.cannotInfer).toBe(true);
    expect(
      parseTypeChoices({ builtIn: [], goTypes: [], examples: [] })?.cannotInfer,
    ).toBeUndefined();
  });

  it('reads no Go types and no examples from an engine that lists none', () => {
    expect(parseTypeChoices({ builtIn: [], crds: [] })).toMatchObject({
      goTypes: [],
      examples: [],
    });
  });

  it("reads the examples of the value's kind, leaving out those it cannot use", () => {
    const example = {
      uri: 'file:///repo/config/samples/limit.yaml',
      file: 'config/samples/limit.yaml',
      line: 1,
      apiVersion: 'rules.example.com/v1',
      kind: 'ReplicaLimit',
    };

    expect(
      parseTypeChoices({
        builtIn: [],
        examples: [example, { ...example, line: 0 }, { ...example, uri: '' }, 'limit.yaml'],
      })?.examples,
    ).toEqual([example]);
  });

  it.each([null, [], { crds: [] }, { builtIn: 'Deployment' }])(
    'reads nothing from %j',
    (answer) => {
      expect(parseTypeChoices(answer)).toBeUndefined();
    },
  );
});

describe('typeLines', () => {
  it('offers a schema, a Go type and a short type, in that order, then the examples in the workspace and Paste an example…, then the built-in kinds, then the CRDs, each group under a separator', () => {
    expect(typeLines(CHOICES)).toEqual([
      WRITE_SCHEMA_LINE,
      goTypeLine(2),
      WRITE_SHORT_TYPE_LINE,
      { separator: true, label: 'Examples in the workspace' },
      {
        label: '$(file) config/samples/limit.yaml',
        description: 'rules.example.com/v1 ReplicaLimit',
        detail: 'Document at line 1',
        value: { action: 'example', uri: 'file:///ws/config/samples/limit.yaml', line: 1 },
      },
      {
        label: '$(file) config/samples/limit.yaml',
        description: 'rules.example.com/v1 ReplicaLimit',
        detail: 'Document at line 12',
        value: { action: 'example', uri: 'file:///ws/config/samples/limit.yaml', line: 12 },
      },
      PASTE_EXAMPLE_LINE,
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
    expect(
      typeLines(CHOICES)
        .slice(0, 3)
        .map((line) => line.label),
    ).toEqual([
      '$(edit) Write a schema in .cellens…',
      '$(symbol-struct) Use a Go type…',
      '$(edit) Write a short type in .cellens…',
    ]);
    expect(PASTE_EXAMPLE_LINE).toMatchObject({
      label: '$(clippy) Paste an example…',
      value: { action: 'paste' },
    });
  });

  it('starts with the same three lines and Paste an example… while the engine lists the kinds', () => {
    expect(firstLines()).toEqual([
      WRITE_SCHEMA_LINE,
      goTypeLine(),
      WRITE_SHORT_TYPE_LINE,
      PASTE_EXAMPLE_LINE,
    ]);
  });

  it("says on the lines that write in .cellens that they open the entry of a value that has one, there and while it loads (#8's review)", () => {
    expect(typeLines(CHOICES, true).slice(0, 3)).toEqual([
      writeLine('schema', true),
      goTypeLine(2),
      writeLine('type', true),
    ]);
    expect(firstLines(true)[0]).toEqual(writeLine('schema', true));
    expect(writeLine('schema', true).detail).toBe(
      'Properties, descriptions and formats, as in a CRD; opens .cellens beside the rule at the entry it has, to write a schema there',
    );
    expect(writeLine('type', true).detail).toBe(
      'Such as {name: string, replicas: int}; opens .cellens beside the rule at the entry it has, to write a short type there',
    );
    expect(writeLine('schema')).toEqual(WRITE_SCHEMA_LINE);
  });

  it('has no group of examples when the workspace has none, and still offers Paste an example…', () => {
    expect(
      typeLines({ ...CHOICES, examples: [] })
        .slice(0, 5)
        .map((line) => line.label),
    ).toEqual([
      '$(edit) Write a schema in .cellens…',
      '$(symbol-struct) Use a Go type…',
      '$(edit) Write a short type in .cellens…',
      '$(clippy) Paste an example…',
      'Built-in kinds · Kubernetes 1.37',
    ]);
  });

  it.each([
    [undefined, undefined],
    [0, 'none in the workspace'],
    [1, '1 type'],
    [12, '12 types'],
  ])('says how many Go types there are on Use a Go type…, with %j: %j', (count, description) => {
    expect(goTypeLine(count).description).toBe(description);
  });

  it('keeps Use a Go type… in the list whatever is typed, so the Go types can be found from the first picker', () => {
    expect(goTypeLine(2)).toMatchObject({ alwaysShow: true, value: { action: 'pickGo' } });
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
    expect(
      typeLines({ ...CHOICES, kubernetesVersion: '', crds: [], examples: [] })
        .slice(4)
        .map((l) => l.label),
    ).toEqual([
      'Built-in kinds',
      '$(symbol-class) Deployment',
      '$(symbol-class) VolumeAttributesClass',
    ]);
  });

  it('is only the ways to write or use a type with no kind and no example at all', () => {
    expect(
      typeLines({ kubernetesVersion: '1.37', builtIn: [], crds: [], goTypes: [], examples: [] }),
    ).toEqual([WRITE_SCHEMA_LINE, goTypeLine(0), WRITE_SHORT_TYPE_LINE, PASTE_EXAMPLE_LINE]);
  });
});

describe('INT_OR_STRING_LINES', () => {
  it("offers only int and string, the two types an x-kubernetes-int-or-string value takes (#8's review)", () => {
    expect(INT_OR_STRING_LINES.map((line) => line.label)).toEqual([
      '$(symbol-numeric) int',
      '$(symbol-string) string',
    ]);
  });
});

describe('goReference', () => {
  it.each([
    ['a folder below', 'file:///ws/.cellens', 'file:///ws/api/v1', './api/v1#QuotaSpec'],
    ['a folder beside', 'file:///ws/deploy/.cellens', 'file:///ws/api/v1', '../api/v1#QuotaSpec'],
    ['the folder itself', 'file:///ws/api/.cellens', 'file:///ws/api', '.#QuotaSpec'],
    ['a folder two up', 'file:///ws/a/b/.cellens', 'file:///ws/api', '../../api#QuotaSpec'],
    ['a folder with a trailing slash', 'file:///ws/.cellens', 'file:///ws/api/', './api#QuotaSpec'],
    [
      'an escaped name, written plain',
      'file:///my%20ws/.cellens',
      'file:///my%20ws/caf%C3%A9/v1',
      './café/v1#QuotaSpec',
    ],
    ['a folder of another host', 'file:///ws/.cellens', 'file://host/ws/api', undefined],
    ['no URI', 'file:///ws/.cellens', '/ws/api', undefined],
    [
      'a Windows drive written two ways',
      'file:///C:/repo/.cellens',
      'file:///c%3A/repo/api/v1',
      './api/v1#QuotaSpec',
    ],
    [
      'a Windows path in another case, written as the folder writes it',
      'file:///c%3A/Repo/.cellens',
      'file:///C:/repo/API/v1',
      './API/v1#QuotaSpec',
    ],
    [
      'a path in another case off Windows',
      'file:///Repo/.cellens',
      'file:///repo/api',
      '../repo/api#QuotaSpec',
    ],
  ])(
    'names a Go type of %s relative to the folder of the .cellens',
    (_name, cellens, folder, expected) => {
      expect(goReference(cellens, folder, 'QuotaSpec')).toBe(expected);
    },
  );
});

describe('the Go types a .cellens can name', () => {
  it.each([
    ['its own folder', 'file:///ws/charts', 'file:///ws/charts', true],
    ['a folder below', 'file:///ws/charts/api/v1', 'file:///ws/charts', true],
    ['another workspace folder', 'file:///ws/operator/api', 'file:///ws/charts', false],
    [
      'a folder whose name starts the same',
      'file:///ws/charts-old/api',
      'file:///ws/charts',
      false,
    ],
    ['a Windows folder written another way', 'file:///c%3A/ws/api', 'file:///C:/WS', true],
    ['another host', 'file://host/ws/charts', 'file:///ws/charts', false],
  ])('finds %s at or below the root: %s', (_name, folder, root, expected) => {
    expect(isAtOrBelow(folder, root)).toBe(expected);
  });

  it("keeps only the Go types of the root, the workspace folder of the .cellens (#8's review)", () => {
    const choices = {
      ...CHOICES,
      goTypes: [
        { folder: 'file:///ws/charts/api', name: 'ChartSpec', doc: '' },
        { folder: 'file:///ws/operator/api/v1', name: 'QuotaSpec', doc: '' },
      ],
    };

    expect(goTypesIn(choices, 'file:///ws/charts').goTypes.map((type) => type.name)).toEqual([
      'ChartSpec',
    ]);
  });

  it.each([
    ['./api/v1#QuotaSpec', 'api/v1#QuotaSpec', true],
    ['./api/v1/#QuotaSpec', 'api//v1#QuotaSpec', true],
    ['.#QuotaSpec', '#QuotaSpec', true],
    ['../api#QuotaSpec', 'api#QuotaSpec', false],
    ['./api/v1#QuotaSpec', './api/v1#Limit', false],
  ])("finds %s and %s the same Go type: %s (#8's review)", (a, b, expected) => {
    expect(sameGo(a, b)).toBe(expected);
  });
});

describe('an engine older than this extension', () => {
  it.each([
    ['no goTypes key', { builtIn: [], crds: [] }, true],
    [
      'goTypes null, as a workspace without Go code may give',
      { builtIn: [], goTypes: null },
      undefined,
    ],
    ['no Go types', { builtIn: [], goTypes: [] }, undefined],
  ])(
    'reads an answer of typeChoices with %s as an outdated engine: %s',
    (_name, answer, expected) => {
      expect(parseTypeChoices(answer)?.outdated).toBe(expected);
    },
  );

  it("says on Use a Go type… that it needs a newer engine, not that there are none (#8's re-review)", () => {
    expect(goTypeLine(0, true).description).toBe('needs a newer engine');
    expect(typeLines({ ...CHOICES, goTypes: [], outdated: true })[1]).toEqual(goTypeLine(0, true));
  });

  /** What the #17 engine, with setType but no snippet, refuses a request with no type with. */
  const NO_TYPE =
    'cel-lens.setType: give the type, such as type: "{name: string}", the kind, such as kind: "apps/v1 Deployment", the schema, such as schema: {"type": "object", "properties": {...}}, or the Go type, such as go: "./api/v1#QuotaSpec"';

  it.each([
    [
      "the InvalidParams of an engine with no snippet, which reads it as a request with no type (#8's re-review)",
      { code: -32602, message: NO_TYPE },
      NO_TYPE.replace('cel-lens.setType: ', ''),
    ],
    [
      'a request with two types',
      {
        code: -32602,
        message: 'cel-lens.setType: give one of type, kind, schema and go, not type and kind',
      },
      undefined,
    ],
    [
      'another refusal that names snippet',
      { code: -32602, message: 'cel-lens.setType: a snippet beside a type' },
      undefined,
    ],
    ['the same words with another code', { code: -32803, message: NO_TYPE }, undefined],
    ['another refusal', { code: -32602, message: 'cel-lens.setType: bad .cellens' }, undefined],
    ['another failure', new Error(NO_TYPE), undefined],
  ])('reads %s as a refused snippet: %j', (_name, error, expected) => {
    expect(snippetRefusal(error)).toBe(expected);
  });

  it('says which setting and command fix it', () => {
    expect(OUTDATED_ENGINE_MESSAGE).toBe(
      'This cel-lens engine is too old to write a type in .cellens or list Go types: set cel-lens.serverPath to a newer cel-lens-core, then run cel-lens: Restart Engine.',
    );
    expect(GO_TYPES_FAILED_LINE.value).toEqual({ action: 'showLogs' });
  });

  it.each([
    [{}, false],
    [{ type: 'int' }, true],
    [{ kind: 'v1 ConfigMap' }, true],
    [{ go: './api#X' }, true],
    [{ schema: { type: 'object' } }, true],
  ])('reads %j as an entry: %s', (entry, expected) => {
    expect(hasEntry(target({ ...CRD_VALUE, ...entry }))).toBe(expected);
  });
});

describe('goTypeLines', () => {
  const folderName = (folder: string): string => folder.replace('file:///ws/', '');

  it('lists each Go type by name, with its folder in the workspace and its doc comment, and names it from the .cellens', () => {
    expect(goTypeLines(CHOICES, 'file:///ws/deploy/.cellens', folderName)).toEqual([
      {
        label: '$(symbol-struct) QuotaSpec',
        description: 'api/v1',
        detail: 'QuotaSpec is the quota of a team.',
        value: { action: 'go', go: '../api/v1#QuotaSpec' },
      },
      {
        label: '$(symbol-struct) Limit',
        description: 'internal/limits',
        value: { action: 'go', go: '../internal/limits#Limit' },
      },
    ]);
  });

  it('says so in one line when there is no Go type', () => {
    expect(goTypeLines({ ...CHOICES, goTypes: [] }, 'file:///ws/.cellens', folderName)).toEqual([
      NO_GO_TYPES_LINE,
    ]);
    expect(NO_GO_TYPES_LINE.value).toEqual({ action: 'none' });
  });

  it('ends with why not every Go type is listed, when the engine says so', () => {
    const note = 'the workspace has more than 1000 types: the first 1000 are listed';

    expect(
      goTypeLines({ ...CHOICES, goTypesNote: note }, 'file:///ws/.cellens', folderName).at(-1),
    ).toEqual({
      label: '$(info) Not every Go type is listed',
      detail: note,
      value: { action: 'none' },
    });
  });
});

describe('isCurrentChoice', () => {
  /** The label of the first line that is what the value has now. */
  function current(lines: readonly PickLine<TypeChoice>[], argument: Record<string, unknown>) {
    const isCurrent = isCurrentChoice(target(argument));
    return lines.find((line) => line.separator !== true && isCurrent(line.value))?.label;
  }

  const policy = { ...POLICY_VALUE, kind: undefined };

  it.each([
    ['the kind it has', POLICY_VALUE, '$(symbol-class) Deployment'],
    ['nothing for a kind not listed', { ...POLICY_VALUE, kind: 'v1 Nothing' }, undefined],
    ['nothing for dyn', CRD_VALUE, undefined],
    [
      'Use a Go type… for a Go type',
      { ...policy, go: './api/v1#QuotaSpec' },
      '$(symbol-struct) Use a Go type…',
    ],
    [
      'Write a schema… for a schema',
      { ...policy, schema: { type: 'object' } },
      '$(edit) Write a schema in .cellens…',
    ],
    [
      'Write a short type… for a short type',
      { ...policy, type: '{name: string}' },
      '$(edit) Write a short type in .cellens…',
    ],
  ])('finds %s', (_name, argument, label) => {
    expect(current(typeLines(CHOICES), argument)).toBe(label);
  });

  it.each([
    ['the int of a CRD value typed int', { ...CRD_VALUE, type: 'int' }, '$(symbol-numeric) int'],
    ['nothing for another type', { ...CRD_VALUE, type: 'bool' }, undefined],
  ])('finds %s', (_name, argument, label) => {
    expect(current(INT_OR_STRING_LINES, argument)).toBe(label);
  });

  it('finds the Go type the value has among the Go types', () => {
    const lines = goTypeLines(CHOICES, 'file:///ws/.cellens', (folder) => folder);

    expect(current(lines, { ...policy, go: './internal/limits#Limit' })).toBe(
      '$(symbol-struct) Limit',
    );
  });
});

describe('parseEntryPlace', () => {
  const selection = { start: { line: 9, character: 11 }, end: { line: 9, character: 14 } };

  it.each([
    ['a new entry', 'added'],
    ['the entry the value has', 'unchanged'],
  ])('reads where the cursor goes in .cellens for %s', (_name, change) => {
    expect(parseEntryPlace({ cellens: 'file:///ws/.cellens', change, selection })).toEqual({
      cellens: 'file:///ws/.cellens',
      selection,
    });
  });

  it.each([
    ['no selection', { cellens: 'file:///ws/.cellens', change: 'added' }],
    ['no cellens', { change: 'added', selection }],
    [
      'a position before the start',
      {
        cellens: 'file:///ws/.cellens',
        change: 'added',
        selection: { ...selection, end: { line: -1, character: 0 } },
      },
    ],
    [
      'a character that is no number',
      {
        cellens: 'file:///ws/.cellens',
        change: 'added',
        selection: { ...selection, start: { line: 9, character: '11' } },
      },
    ],
    ['null', null],
  ])('reads nothing from an answer with %s', (_name, answer) => {
    expect(parseEntryPlace(answer)).toBeUndefined();
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
  it.each([
    ['its Go type', { go: './api/v1#QuotaSpec' }, './api/v1#QuotaSpec'],
    ['schema for a schema', { schema: { type: 'object' } }, 'schema'],
  ])('shows a value with %s', (_name, entry, description) => {
    const typed = target({ ...POLICY_VALUE, kind: undefined, ...entry });

    expect(
      valueLines([{ target: typed, command: 'cel-lens.editType', line: 30 }])[0]?.description,
    ).toBe(description);
  });

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
