import { describe, expect, it } from 'vitest';

import {
  configurationParams,
  effectiveVersion,
  parseVersionInfo,
  pickItems,
  serverOptions,
  statusText,
  statusTooltip,
  unsupportedVersionMessage,
} from './kubernetes_version';

const INFO = { inUse: '1.37', defaultVersion: '1.37', supported: ['1.37', '1.36'] };
const UNLISTED = { inUse: '1.37', defaultVersion: '1.37', supported: [] };

function initializeResult(celLens: unknown): unknown {
  return { capabilities: { experimental: { celLens } } };
}

describe('statusText', () => {
  it.each([
    ['1.37', 'CEL · K8s 1.37'],
    ['1.30', 'CEL · K8s 1.30'],
  ])('shows version %s as %s', (version, text) => {
    expect(statusText(version)).toBe(text);
  });
});

describe('statusTooltip', () => {
  it('says cel-lens matches the API server of that version', () => {
    expect(statusTooltip('1.37')).toBe('cel-lens: Kubernetes 1.37 · click for actions');
  });
});

describe('serverOptions', () => {
  it.each([
    ['', { kubernetesVersion: '' }],
    ['   ', { kubernetesVersion: '' }],
    ['1.35', { kubernetesVersion: '1.35' }],
    [' 1.35 ', { kubernetesVersion: '1.35' }],
    ['v1.35', { kubernetesVersion: '1.35' }],
  ])('maps the setting %j to %j', (setting, options) => {
    expect(serverOptions(setting)).toEqual(options);
  });
});

describe('serverOptions with the engine versions', () => {
  it.each([
    ['1.36', { kubernetesVersion: '1.36' }],
    ['1.99', { kubernetesVersion: '' }],
    ['v1.36', { kubernetesVersion: '1.36' }],
    ['v1.99', { kubernetesVersion: '' }],
    ['', { kubernetesVersion: '' }],
  ])('maps the setting %j to %j', (setting, options) => {
    expect(serverOptions(setting, INFO)).toEqual(options);
  });

  it('trusts a server that lists no versions', () => {
    expect(serverOptions('1.99', UNLISTED)).toEqual({
      kubernetesVersion: '1.99',
    });
  });
});

describe('configurationParams', () => {
  it('sends an empty version for an unsupported setting', () => {
    expect(configurationParams('1.99', INFO)).toEqual({ settings: { kubernetesVersion: '' } });
  });

  it.each([
    ['', { settings: { kubernetesVersion: '' } }],
    ['1.36', { settings: { kubernetesVersion: '1.36' } }],
  ])('wraps the setting %j as %j', (setting, params) => {
    expect(configurationParams(setting)).toEqual(params);
  });
});

describe('effectiveVersion', () => {
  it.each([
    ['', '1.37'],
    ['1.36', '1.36'],
    [' 1.36 ', '1.36'],
    ['1.99', '1.37'],
    ['v1.36', '1.36'],
    ['v1.99', '1.37'],
  ])('uses %j to show %s', (setting, version) => {
    expect(effectiveVersion(setting, INFO)).toBe(version);
  });
});

describe('effectiveVersion with a server that lists no versions', () => {
  it.each([
    ['1.99', '1.37'],
    ['', '1.37'],
    ['1.34', '1.34'],
  ])('with the setting %j shows only the version in use, %s', (setting, inUse) => {
    expect(effectiveVersion(setting, { ...UNLISTED, inUse, defaultVersion: '1.37' })).toBe(inUse);
  });
});

describe('unsupportedVersionMessage', () => {
  it.each([['1.99'], ['v1.99']])(
    'names the supported versions when the setting is %j',
    (setting) => {
      const message = unsupportedVersionMessage(setting, INFO);

      expect(message).toContain(`"${setting}"`);
      expect(message).toContain('1.37, 1.36');
    },
  );

  it.each([[''], ['1.36'], ['v1.36']])('is silent when the setting is %j', (setting) => {
    expect(unsupportedVersionMessage(setting, INFO)).toBeUndefined();
  });

  it.each([
    ['1.99', '1.37', true],
    ['1.34', '1.34', false],
    ['v1.34', '1.34', false],
    ['v1.99', '1.37', true],
    ['', '1.37', false],
  ])(
    'with no list, the setting %j and version %s in use: refused is %s',
    (setting, inUse, refused) => {
      const message = unsupportedVersionMessage(setting, { ...UNLISTED, inUse });

      expect(message !== undefined).toBe(refused);
    },
  );
});

describe('parseVersionInfo', () => {
  it('reads the default and the supported versions', () => {
    const result = initializeResult({
      kubernetesVersion: '1.37',
      supportedKubernetesVersions: ['1.37', '1.36'],
    });

    expect(parseVersionInfo(result, '')).toEqual(INFO);
  });

  it('drops supported entries that are not versions', () => {
    const result = initializeResult({
      kubernetesVersion: '1.37',
      supportedKubernetesVersions: ['1.37', 3, ''],
    });

    expect(parseVersionInfo(result, '')?.supported).toEqual(['1.37']);
  });

  it.each([
    [
      'cel-lens-core v0.1.0',
      {
        kubernetesVersion: '1.37',
        capabilities: { hoverProvider: true },
        serverInfo: { name: 'cel-lens-core', version: 'v0.1.0' },
      },
    ],
    ['a top-level version only', { kubernetesVersion: '1.36' }],
  ])('reads the top-level version from %s, with no supported list', (_name, result) => {
    expect(parseVersionInfo(result, '')).toEqual({
      inUse: result.kubernetesVersion,
      defaultVersion: result.kubernetesVersion,
      supported: [],
    });
  });

  it.each([['1.34'], ['1.99']])(
    'does not take the top-level version as the default when the server was started with %s',
    (sent) => {
      expect(parseVersionInfo({ kubernetesVersion: '1.34' }, sent)).toEqual({
        inUse: '1.34',
        defaultVersion: undefined,
        supported: [],
      });
    },
  );

  it.each([
    ['undefined', undefined],
    ['null', null],
    ['no capabilities', {}],
    ['no experimental', { capabilities: {} }],
    ['no celLens', initializeResult(undefined)],
    ['no version', initializeResult({})],
    ['an empty version', initializeResult({ kubernetesVersion: '' })],
    ['a numeric version', initializeResult({ kubernetesVersion: 1.37 })],
  ])('gives nothing for %s', (_name, result) => {
    expect(parseVersionInfo(result, '')).toBeUndefined();
  });
});

describe('pickItems', () => {
  it.each([
    ['', 'current'],
    ['1.34', ''],
    ['v1.34', ''],
    ['1.99', 'current'],
  ])(
    'with a pinned start and the setting %j, names no default and marks it %j',
    (setting, mark) => {
      const info = { inUse: '1.34', defaultVersion: undefined, supported: [] };

      expect(pickItems(info, setting)).toEqual([
        { label: 'Default', description: mark, version: '' },
      ]);
    },
  );

  it.each([
    ['1.34', '1.34'],
    ['v1.34', '1.34'],
  ])('with the setting %j marks version %j as current, not the default', (setting, marked) => {
    const info = { inUse: marked || '1.37', defaultVersion: '1.37', supported: ['1.37', '1.34'] };
    const current = pickItems(info, setting).filter((item) => item.description === 'current');

    expect(current.map((item) => item.version)).toEqual([marked]);
  });

  it('lists the default first, then every supported version, marking the default as current', () => {
    expect(pickItems(INFO, '')).toEqual([
      { label: 'Default (1.37)', description: 'current', version: '' },
      { label: '1.37', description: '', version: '1.37' },
      { label: '1.36', description: '', version: '1.36' },
    ]);
  });

  it('marks the pinned version as current', () => {
    expect(pickItems(INFO, '1.36')).toEqual([
      { label: 'Default (1.37)', description: '', version: '' },
      { label: '1.37', description: '', version: '1.37' },
      { label: '1.36', description: 'current', version: '1.36' },
    ]);
  });
});
