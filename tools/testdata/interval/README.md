# Interval golden cases

`golden.json` holds 515 cases from Grafana's `calculateInterval`. Each has a time span in milliseconds and a step count. Cases with `min` also have a lower limit. `interval` and `interval_ms` are what Grafana returned. `TestIntervalMatchesGrafana` checks `$__interval`, `$__interval_ms` and `$__auto` with each case's step count and minimum (the panel's max data points and min interval), and `grafanaCalculateInterval` for the cases with `min` (as used for `auto` interval variables).

The file is data only. It contains no Grafana code.

## How the cases were produced

`calculateInterval`, `roundInterval`, `secondsToHms` and `intervalToMs` were run in Node, taken as written from [rangeutil.ts@b338b18](https://github.com/grafana/grafana/blob/b338b18248d25c04fe9afe5a631ae7472716309b/packages/grafana-data/src/datetime/rangeutil.ts) (the `source` field repeats this). Dashboards call it from [`SceneQueryRunner.prepareRequests`](https://github.com/grafana/scenes/blob/22a0dbe1898c6318602e8a93c114109fd5325887/packages/scenes/src/querying/SceneQueryRunner.ts) as `calculateInterval(range, maxDataPoints, minInterval ?? ds.interval)`.

The spans are 18 hand-picked ranges from 1s to about 14 years, plus every `roundInterval` threshold (x100, because the step count is 100) and one millisecond either side of it. Each span also runs with step counts and lower limits 30/`10s`, 30/`1m`, 50/`1h` and 100/`1ms`.

Not covered: the panel's own max data points and min interval, the datasource's min interval, and `$__rate_interval`.

To regenerate, run those functions over the same spans. The generator copies Grafana's AGPL-licensed functions, so it isn't committed here; it can be shared on request.
