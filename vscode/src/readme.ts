/** The text under the `## <heading>` line, up to the next `## ` heading. */
export function readmeSection(readme: string, heading: string): string | undefined {
  const lines = readme.split('\n');
  const start = lines.indexOf(`## ${heading}`);
  if (start === -1) {
    return undefined;
  }
  const after = lines.slice(start + 1);
  const length = after.findIndex((line) => line.startsWith('## '));
  return (length === -1 ? after : after.slice(0, length)).join('\n');
}

/** The level-two headings, in order. */
export function readmeHeadings(readme: string): string[] {
  return readme
    .split('\n')
    .filter((line) => line.startsWith('## '))
    .map((line) => line.slice(3));
}

export interface ReadmeImage {
  readonly alt: string;
  readonly url: string;
}

/** Every Markdown image `![alt](url)` in the text. */
export function readmeImages(readme: string): ReadmeImage[] {
  return [...readme.matchAll(/!\[([^\]]*)\]\(([^)\s]+)\)/g)].map((found) => ({
    alt: found[1] ?? '',
    url: found[2] ?? '',
  }));
}

/** The distinct Kubernetes versions such as `1.37` written in backticks, in order. */
export function readmeKubernetesVersions(section: string): string[] {
  return [...new Set([...section.matchAll(/`(1\.\d+)`/g)].map((found) => found[1] ?? ''))];
}

/** The Default cell of each row of the settings table, by setting name, as written (without the backticks). */
export function readmeSettingDefaults(section: string): Map<string, string> {
  const defaults = new Map<string, string>();
  for (const line of section.split('\n')) {
    const found = /^\|\s*`([^`]+)`\s*\|\s*`([^`]*)`\s*\|/.exec(line);
    if (found?.[1] !== undefined && found[2] !== undefined) {
      defaults.set(found[1], found[2]);
    }
  }
  return defaults;
}

/** The version in "The default is `1.37`." in the text, if it states one. */
export function readmeDefaultKubernetesVersion(section: string): string | undefined {
  return /\bdefault is `(1\.\d+)`/i.exec(section)?.[1];
}
