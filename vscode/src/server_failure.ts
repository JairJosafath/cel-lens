import type { ServerSource } from './server_path';

/** How much of the server's stderr a failure message quotes, so the message stays readable. */
export const STDERR_LIMIT = 2000;

/** Keeps the end of what a process wrote to stderr, which is where it says why it stopped. */
export class StderrTail {
  private text = '';

  add(chunk: string): void {
    this.text = (this.text + chunk).slice(-STDERR_LIMIT);
  }

  toString(): string {
    return this.text.trim();
  }
}

/** How many of the engine's last lines, and how long each, a failure message quotes. */
export const QUOTED_LINES = 5;
export const QUOTED_LINE_LENGTH = 200;

/** Errors of the pipe to an engine that is already gone: a result of the failure, never its cause. */
const STREAM_ERROR = /EPIPE|write after a stream was destroyed|ERR_STREAM_DESTROYED/i;

/** What the engine run left behind: its words, why it could not be started, and how it ended. */
export class StartTrace {
  readonly stderr = new StderrTail();
  spawnError: { code: string | undefined; message: string } | undefined;
  exitCode: number | undefined;
  /** Settles when the spawned engine has closed its streams or failed to spawn; undefined before any spawn. */
  ended: Promise<void> | undefined;

  constructor(readonly command: string) {}
}

function spawnCause(error: { code: string | undefined; message: string }, command: string): string {
  if (error.code === 'EACCES') {
    return `the engine file can't be run (permission denied): ${command}`;
  }
  if (error.code === 'ENOENT') {
    return `the engine file was not found: ${command}`;
  }
  if (error.code === 'ENOEXEC') {
    return `the engine file is not a program that can be run: ${command}`;
  }
  return `the engine file can't be started (${error.message}): ${command}`;
}

function lastLines(output: string): string {
  return output
    .split('\n')
    .map((line) => line.trim())
    .filter((line) => line !== '')
    .slice(-QUOTED_LINES)
    .map((line) =>
      line.length > QUOTED_LINE_LENGTH ? `${line.slice(0, QUOTED_LINE_LENGTH)}…` : line,
    )
    .map((line) => line.replace(/["\\]/g, '\\$&'))
    .join(' / ');
}

function withoutFullStop(text: string): string {
  return text.trim().replace(/\.+$/, '');
}

/** The message for a server that did not start: its real cause first, then what to do. */
export function startFailureMessage(
  reason: string,
  trace: StartTrace,
  source: ServerSource,
): string {
  const quoted = lastLines(trace.stderr.toString());
  const ownReason = trace.spawnError === undefined && !STREAM_ERROR.test(reason);
  let cause: string;
  if (trace.spawnError !== undefined) {
    cause = spawnCause(trace.spawnError, trace.command);
  } else if (quoted !== '') {
    cause = ownReason ? `"${quoted}" (${withoutFullStop(reason)})` : `"${quoted}"`;
  } else if (ownReason) {
    cause = withoutFullStop(reason);
  } else {
    cause =
      trace.exitCode === undefined
        ? 'the engine stopped before it was ready'
        : `the engine exited with code ${String(trace.exitCode)}`;
  }
  const hint =
    source === 'setting'
      ? 'Fix the cel-lens.serverPath setting, or clear it to use the bundled engine.'
      : 'Show the logs for details, then reinstall the extension.';
  return `The cel-lens engine failed to start: ${cause}. ${hint}`;
}
