# extract.Find benchmarks

Measured 2026-10-09 on an AMD Ryzen 7 3700U (linux/amd64), Go 1.26.9.

- before: main, `51a5111`, with this branch's `extract_test.go` copied in so the same benchmarks run
- after: HEAD, `f901814` (main merged with `c103d6d`)

Each number is the median of 5 runs, alternating main and HEAD (main, HEAD, main, HEAD, ...).
Main takes 25 to 55 s per operation on the first three forms, so those use one iteration per run.

| Form (K = 4000 schemas) | ns/op before | ns/op after | B/op before | B/op after | allocs/op before | allocs/op after |
|---|---:|---:|---:|---:|---:|---:|
| alias | 25,811,103,863 | 414,387,214 | 2,738,968,776 | 36,900,008 | 80,361,411 | 392,944 |
| merge-schema | 24,970,462,538 | 343,748,265 | 2,737,596,288 | 35,523,328 | 80,357,441 | 388,945 |
| merge-properties | 54,098,296,948 | 472,210,268 | 8,160,763,832 | 48,770,136 | 80,985,256 | 561,055 |
| plain | 2,048,015,778 | 1,909,428,214 | 173,426,824 | 171,455,056 | 1,495,497 | 1,511,507 |
| anchored | 3,488,777,599 | 3,059,778,951 | 295,193,829 | 258,582,837 | 3,128,157 | 2,684,542 |

alias, merge-schema and merge-properties are `BenchmarkFindSharedProperties` with 4000 schemas
over one 4000-entry properties map. plain and anchored are `BenchmarkFindManyKeys`
(`anchored=false` and `anchored=true`, K=4000). Plain and anchored have no shared map, so they
only show that the change does not slow them down.

Commands, run once per round for each of the two test binaries (built with `go test -c`):

    go test -run '^$' -bench 'FindSharedProperties/(alias|merge-schema|merge-properties)$' -benchmem -benchtime 1x ./pkg/extract/
    go test -run '^$' -bench 'FindManyKeys/anchored=.*/K=4000$' -benchmem -benchtime 3x ./pkg/extract/
