import { describe, expect, it } from 'vitest';

import { STDERR_LIMIT, StartTrace, StderrTail, startFailureMessage } from './server_failure';

describe('StderrTail', () => {
  it.each([
    { chunks: [], expected: '' },
    { chunks: ['boom\n'], expected: 'boom' },
    { chunks: ['unknown ', 'flag --stdio\n'], expected: 'unknown flag --stdio' },
  ])('holds $chunks as "$expected"', ({ chunks, expected }) => {
    const tail = new StderrTail();
    for (const chunk of chunks) {
      tail.add(chunk);
    }
    expect(tail.toString()).toBe(expected);
  });

  it('keeps only the end of a long output', () => {
    const tail = new StderrTail();
    tail.add('x'.repeat(STDERR_LIMIT));
    tail.add('the end');
    expect(tail.toString()).toHaveLength(STDERR_LIMIT);
    expect(tail.toString().endsWith('the end')).toBe(true);
  });
});

function traceOf(
  stderr: string,
  extra: {
    spawnError?: { code: string | undefined; message: string } | undefined;
    exitCode?: number | undefined;
  } = {},
): StartTrace {
  const trace = new StartTrace('/opt/engine');
  trace.stderr.add(stderr);
  trace.spawnError = extra.spawnError;
  trace.exitCode = extra.exitCode;
  return trace;
}

describe('startFailureMessage', () => {
  it('leads with the engine output and says what to do', () => {
    const message = startFailureMessage(
      'Server process exited with code 2',
      traceOf('unknown flag: --stdio\n'),
      'bundled',
    );
    expect(message).toBe(
      'The cel-lens engine failed to start: "unknown flag: --stdio" (Server process exited with code 2). Show the logs for details, then reinstall the extension.',
    );
    expect(message).not.toContain('serverPath');
  });

  it('says to fix or clear the setting when the binary came from it', () => {
    const message = startFailureMessage('it stopped', traceOf(''), 'setting');
    expect(message).toContain('Fix the cel-lens.serverPath setting, or clear it');
    expect(message).not.toContain('reinstall');
  });

  it('has no empty line when the server wrote nothing', () => {
    expect(startFailureMessage('it stopped', traceOf('  '), 'bundled')).toBe(
      'The cel-lens engine failed to start: it stopped. Show the logs for details, then reinstall the extension.',
    );
  });

  it('separates lines with a slash and escapes double quotes in the quoted output', () => {
    expect(
      startFailureMessage('stopped', traceOf('bad flag "--serve"\nusage: engine\\n'), 'bundled'),
    ).toBe(
      'The cel-lens engine failed to start: "bad flag \\"--serve\\" / usage: engine\\\\n" (stopped). Show the logs for details, then reinstall the extension.',
    );
  });

  it('quotes only the last lines of a long output, each kept short', () => {
    const lines = Array.from({ length: 12 }, (_, n) => `line ${String(n)}`);
    lines.push('y'.repeat(500));
    const message = startFailureMessage('stopped', traceOf(lines.join('\n')), 'bundled');
    expect(message).not.toContain('line 7');
    expect(message).toContain('line 8 / line 9');
    expect(message).toContain(`${'y'.repeat(200)}…`);
    expect(message).not.toContain('y'.repeat(201));
  });

  it.each([
    { code: 'EACCES', words: "can't be run (permission denied): /opt/engine" },
    { code: 'ENOENT', words: 'was not found: /opt/engine' },
    { code: 'ENOEXEC', words: 'is not a program that can be run: /opt/engine' },
    { code: undefined, words: "can't be started (spawn boom): /opt/engine" },
  ])('explains a spawn error $code in plain words', ({ code, words }) => {
    const message = startFailureMessage(
      'Cannot call write after a stream was destroyed',
      traceOf('', { spawnError: { code, message: 'spawn boom' } }),
      'setting',
    );
    expect(
      message.startsWith(`The cel-lens engine failed to start: the engine file ${words}. `),
    ).toBe(true);
    expect(message).not.toContain('stream was destroyed');
  });

  it.each([
    { reason: 'write EPIPE', exitCode: 3, plain: 'the engine exited with code 3' },
    { reason: 'Cannot call write after a stream was destroyed', exitCode: 1, plain: 'code 1' },
    { reason: 'write EPIPE', exitCode: undefined, plain: 'stopped before it was ready' },
  ])('hides the stream error "$reason" and gives a plain reason', ({ reason, exitCode, plain }) => {
    const message = startFailureMessage(reason, traceOf('', { exitCode }), 'bundled');
    expect(message).toContain(`${plain}. Show the logs`);
    expect(message).not.toMatch(/EPIPE|stream was destroyed/);
  });

  it('leaves a stream error out when the engine printed something', () => {
    const message = startFailureMessage('write EPIPE', traceOf('bad flag\n'), 'bundled');
    expect(message).toBe(
      'The cel-lens engine failed to start: "bad flag". Show the logs for details, then reinstall the extension.',
    );
    expect(message).not.toContain('EPIPE');
  });
});
