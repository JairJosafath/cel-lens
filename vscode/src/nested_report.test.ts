import { describe, expect, it } from 'vitest';
import { describeFailures, type NestedReport } from './nested_report';

describe('describeFailures', () => {
  it.each([
    {
      name: 'names the file and title of a failed test',
      report: {
        testResults: [
          {
            name: '/c/src/a.test.ts',
            assertionResults: [
              { title: 'works', status: 'passed' },
              { title: 'breaks', status: 'failed', failureMessages: ['boom'] },
            ],
          },
        ],
      } satisfies NestedReport,
      expected: 'src/a.test.ts > breaks\nboom',
    },
    {
      name: 'names a file that failed to load',
      report: {
        testResults: [{ name: '/c/src/b.test.ts', message: 'no import', assertionResults: [] }],
      } satisfies NestedReport,
      expected: 'src/b.test.ts\nno import',
    },
    {
      name: 'is empty when nothing failed',
      report: {
        testResults: [
          { name: '/c/src/a.test.ts', assertionResults: [{ title: 'ok', status: 'passed' }] },
        ],
      } satisfies NestedReport,
      expected: '',
    },
  ])('$name', ({ report, expected }) => {
    expect(describeFailures(report, '/c')).toBe(expected);
  });
});
