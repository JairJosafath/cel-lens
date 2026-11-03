function asRecord(value: unknown): Record<string, unknown> | undefined {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
    ? Object.fromEntries(Object.entries(value))
    : undefined;
}

/** The enum of the `cel-lens.experimental` items in a parsed `package.json`; `[]` when missing or malformed. */
export function experimentalSettingEnum(manifest: unknown): string[] {
  const configuration = asRecord(asRecord(asRecord(manifest)?.['contributes'])?.['configuration']);
  const setting = asRecord(asRecord(configuration?.['properties'])?.['cel-lens.experimental']);
  const names = asRecord(setting?.['items'])?.['enum'];
  return Array.isArray(names) && names.every((name) => typeof name === 'string')
    ? names.filter((name): name is string => typeof name === 'string')
    : [];
}
