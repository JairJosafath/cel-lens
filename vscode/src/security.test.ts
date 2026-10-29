import { existsSync, readFileSync } from 'node:fs';

import { describe, expect, it } from 'vitest';

const SECURITY = existsSync('SECURITY.md') ? readFileSync('SECURITY.md', 'utf8') : '';

describe('SECURITY.md', () => {
  it('exists', () => {
    expect(SECURITY).not.toBe('');
  });

  it.each([
    'private vulnerability reporting',
    'JairJosafath/cel-lens',
    'public issue',
    'within 7 days',
    'within 14 days',
    'patch release',
    'CHANGELOG',
  ])('says %s', (text) => {
    expect(SECURITY.replace(/\s+/g, ' ').toLowerCase()).toContain(text.toLowerCase());
  });

  it.each(['SECURITY.md', 'README.md'])('%s does not name the factory', (file) => {
    expect(readFileSync(file, 'utf8')).not.toMatch(/factory|c4|git[e]a/i);
  });
});
