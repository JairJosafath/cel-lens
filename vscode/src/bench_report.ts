import type { Bench } from 'tinybench';

/** Runs the bench and prints the median time (steady against GC pauses) of each task. */
export async function runAndReport(bench: Bench): Promise<void> {
  await bench.run();
  for (const task of bench.tasks) {
    const { result } = task;
    if (result.state === 'completed') {
      process.stdout.write(`${task.name}: ${result.latency.p50.toFixed(1)} ms per run (median)\n`);
    }
  }
}
