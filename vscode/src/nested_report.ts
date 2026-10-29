export interface NestedReport {
  testResults: {
    name: string;
    message?: string;
    assertionResults: { title: string; status: string; failureMessages?: string[] }[];
  }[];
}

/** One block per failed test (or failed file), naming the file and the test title. */
export function describeFailures(report: NestedReport, base: string): string {
  const blocks: string[] = [];
  for (const file of report.testResults) {
    const name = file.name.startsWith(base) ? file.name.slice(base.length + 1) : file.name;
    const failed = file.assertionResults.filter((test) => test.status === 'failed');
    for (const test of failed) {
      blocks.push(`${name} > ${test.title}\n${(test.failureMessages ?? []).join('\n')}`);
    }
    if (failed.length === 0 && file.message !== undefined && file.message !== '') {
      blocks.push(`${name}\n${file.message}`);
    }
  }
  return blocks.join('\n\n');
}
