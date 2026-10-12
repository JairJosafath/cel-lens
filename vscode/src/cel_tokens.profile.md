# tokenizeCel growth profile (issue #170)

Date: 2026-10-09. Node with `--expose-gc`, one process per size. The bench file runs all
sizes in one process, so one size per process was profiled instead with `npm run profile`
(`src/cel_tokens.profile.ts`: `generateCrd(n)`, 10 warm-up runs, then 60 runs with a forced GC
before each, bundled with esbuild). It writes the `.cpuprofile` to `out/profile/`:

```
npm run profile -- 5000   # and 10000
node --expose-gc --trace-gc out/profile/cel_tokens.profile.mjs 5000   # and 10000, after npm run profile
```

The script prints the median per profiled run, the run counts and the profile directory:

```
Profiled 5000 rules: median 113.3 ms per run (10 warm-up runs, 60 profiled runs).
CPU profile written to out/profile/
Profiled 10000 rules: median 150.2 ms per run (10 warm-up runs, 60 profiled runs).
CPU profile written to out/profile/
```

That output is from a rerun on a busier machine. The table below comes from the first
measurement, which had a median per run of 59.5 ms at 5000 rules and 121.4 ms at 10000 rules
(2.04x). Absolute times move with the machine; the growth between sizes is what to compare.

Self time over all 70 runs (ms, share of the profile). `offsetMap`, the locator and `lineStarts`
are inlined into `forEachCelToken`, so they show under it. The push is the callback in `tokenizeCel`.

| Step                        | 5000 rules   | 10000 rules  | Growth |
| --------------------------- | ------------ | ------------ | ------ |
| scanCel                     | 1967 (25.6%) | 4497 (30.1%) | 2.29x  |
| push in tokenizeCel         | 1462 (19.0%) | 2590 (17.3%) | 1.77x  |
| forEachCelToken (+ inlined) | 697 (9.1%)   | 2121 (14.2%) | 3.04x  |
| findCel                     | 616 (8.0%)   | 1116 (7.5%)  | 1.81x  |
| garbage collector           | 1663 (21.7%) | 2449 (16.4%) | 1.47x  |
| whole profile               | 7674         | 14965        | 1.95x  |

`--trace-gc`: 63 Mark-Compact (forced) and 14 vs 16 Scavenges at 5000 vs 10000 rules; the GC
share of the profile falls from 21.7% to 16.4%, so GC does not make the growth superlinear.

Reading: every step grows about 2x within sampling noise (the whole profile 1.95x). The
2.3-2.9x seen before was GC or machine noise, so no rewrite is needed. The `forEachCelToken`
figure (10 ms to 30 ms per run) is the one above 2x, but it is inlined code and its sampling
error is large.
