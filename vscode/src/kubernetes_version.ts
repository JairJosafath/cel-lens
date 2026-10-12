/** The `cel-lens.kubernetesVersion` setting, as contributed in package.json. */
export const KUBERNETES_VERSION_SETTING = 'kubernetesVersion';

/** The `cel-lens.pickKubernetesVersion` command, as contributed in package.json. */
export const PICK_VERSION_COMMAND = 'cel-lens.pickKubernetesVersion';

/** The `cel-lens.restartServer` command, as contributed in package.json. */
export const RESTART_SERVER_COMMAND = 'cel-lens.restartServer';

/** What the language server is doing, as far as the version picker can tell. */
export type ServerState =
  | { readonly kind: 'stopped' }
  | { readonly kind: 'starting' }
  | { readonly kind: 'failed' }
  /** Started, then died, and the client gave up restarting it. */
  | { readonly kind: 'crashed' }
  /** Running; `info` is `undefined` when the server reports no Kubernetes versions. */
  | { readonly kind: 'running'; readonly info: VersionInfo | undefined };

/** What the server's `initialize` result says about Kubernetes versions. */
export interface VersionInfo {
  /** The version the engine uses after `initializationOptions`, such as `1.37`. */
  readonly inUse: string;
  /** The version the engine uses when the setting is empty; `undefined` when not yet learned. */
  readonly defaultVersion: string | undefined;
  /** Every version the engine can match, newest first; empty when the server does not say. */
  readonly supported: readonly string[];
}

/** What the server is told: an empty `kubernetesVersion` returns it to the engine's default. */
export interface ServerOptions {
  readonly kubernetesVersion: string;
}

/** The parameters of `workspace/didChangeConfiguration`. */
export interface ConfigurationParams {
  readonly settings: ServerOptions;
}

/** One line of the version quick pick; an empty `version` means the default. */
export interface VersionPickItem {
  readonly label: string;
  readonly description: string;
  readonly version: string;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null;
}

/**
 * Reads the versions out of the server's `initialize` result. cel-lens-core
 * v0.1.0 reports the version in use as `kubernetesVersion` at the top level and
 * no list; that is the engine default only when `sentVersion`, what the
 * server was started with, was empty. A server that also reports
 * `capabilities.experimental.celLens` is read from there, with its default and
 * supported versions. Anything else gives `undefined`.
 */
export function parseVersionInfo(
  initializeResult: unknown,
  sentVersion: string,
): VersionInfo | undefined {
  if (!isRecord(initializeResult)) {
    return undefined;
  }
  const capabilities = initializeResult['capabilities'];
  const experimental = isRecord(capabilities) ? capabilities['experimental'] : undefined;
  const celLens = isRecord(experimental) ? experimental['celLens'] : undefined;
  const topLevel = initializeResult['kubernetesVersion'];
  if (isRecord(celLens) && isVersion(celLens['kubernetesVersion'])) {
    const listed = celLens['supportedKubernetesVersions'];
    return {
      inUse: isVersion(topLevel) ? topLevel : celLens['kubernetesVersion'],
      defaultVersion: celLens['kubernetesVersion'],
      supported: Array.isArray(listed) ? listed.filter(isVersion) : [],
    };
  }
  return isVersion(topLevel)
    ? { inUse: topLevel, defaultVersion: sentVersion === '' ? topLevel : undefined, supported: [] }
    : undefined;
}

function isVersion(value: unknown): value is string {
  return typeof value === 'string' && value !== '';
}

/**
 * Whether the engine refuses the setting. A server that lists its versions is
 * judged by the list; one that does not (v0.1.0) is judged by the version it
 * reported in use, which is current because a change restarts it.
 */
function isRefused(pinned: string, info: VersionInfo): boolean {
  if (pinned === '') {
    return false;
  }
  return info.supported.length > 0
    ? !info.supported.includes(pinned)
    : pinned !== withoutLeadingV(info.inUse);
}

/** cel-lens-core accepts `v1.34` and reports `1.34`, so the two are the same version. */
function withoutLeadingV(version: string): string {
  return version.startsWith('v') ? version.slice(1) : version;
}

/** The setting as the engine names it: trimmed and without a leading `v`. */
function pinnedVersion(setting: string): string {
  return withoutLeadingV(setting.trim());
}

/**
 * The version results match: the setting if the engine accepts it, otherwise
 * the engine's default. A server that lists no versions is believed about what
 * it uses.
 */
export function effectiveVersion(setting: string, info: VersionInfo): string {
  const pinned = pinnedVersion(setting);
  if (info.supported.length === 0) {
    return info.inUse;
  }
  return pinned === '' || isRefused(pinned, info) ? (info.defaultVersion ?? info.inUse) : pinned;
}

/** Says the engine refuses the setting; `undefined` when it is fine. */
export function unsupportedVersionMessage(setting: string, info: VersionInfo): string | undefined {
  if (!isRefused(pinnedVersion(setting), info)) {
    return undefined;
  }
  const pinned = setting.trim();
  const supported =
    info.supported.length > 0 ? ` Supported versions: ${info.supported.join(', ')}.` : '';
  return `cel-lens.kubernetesVersion is "${pinned}", which the engine cannot match. Using ${effectiveVersion(setting, info)}.${supported}`;
}

/** The status bar text, such as `CEL · K8s 1.37`. */
export function statusText(version: string): string {
  return `CEL · K8s ${version}`;
}

/** The status bar text while the engine is down. */
export const ENGINE_DOWN_TEXT = '$(error) CEL · Engine down';

/** The status bar tooltip while the engine is down, with the reason for the state. */
export function engineDownTooltip(state: 'failed' | 'crashed'): string {
  const reason =
    state === 'failed' ? 'the engine failed to start' : 'the engine stopped after crashing';
  return `cel-lens: ${reason} · click to restart it or show the logs`;
}

/** The status bar tooltip. */
export function statusTooltip(version: string): string {
  return `cel-lens: Kubernetes ${version} · click for actions`;
}

/**
 * Maps the setting to what the server is given; empty returns the engine to its
 * default. A version `info` says the engine refuses is replaced by the empty
 * one, so the server and the status bar agree. A server that lists no versions
 * is given the setting and answers with the version it uses.
 */
export function serverOptions(setting: string, info?: VersionInfo): ServerOptions {
  const pinned = pinnedVersion(setting);
  const listed = info !== undefined && info.supported.length > 0;
  return { kubernetesVersion: listed && isRefused(pinned, info) ? '' : pinned };
}

/** The `workspace/didChangeConfiguration` parameters for the setting. */
export function configurationParams(setting: string, info?: VersionInfo): ConfigurationParams {
  return { settings: serverOptions(setting, info) };
}

/**
 * The quick pick lines: the engine's default first, then every supported
 * version. The default is named only when it is known.
 */
export function pickItems(info: VersionInfo, setting: string): VersionPickItem[] {
  const current = effectiveVersion(setting, info);
  const pinned = pinnedVersion(setting);
  return [
    {
      label: info.defaultVersion === undefined ? 'Default' : `Default (${info.defaultVersion})`,
      description: pinned === '' || current !== pinned ? 'current' : '',
      version: '',
    },
    ...info.supported.map((version) => ({
      label: version,
      description: pinned !== '' && version === current ? 'current' : '',
      version,
    })),
  ];
}
