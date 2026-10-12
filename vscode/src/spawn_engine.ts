import { spawn, type ChildProcessWithoutNullStreams } from 'node:child_process';

/** Runs the engine as a language server over stdio; the extension and the e2e test both start it here. */
export function spawnEngine(
  command: string,
  args: readonly string[],
): ChildProcessWithoutNullStreams {
  return spawn(command, [...args], { stdio: ['pipe', 'pipe', 'pipe'] });
}
