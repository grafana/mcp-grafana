# Variable interpolation golden cases

`golden.json` holds 1,600 cases (320 each for prometheus, loki, postgres, mysql and mssql). Each case is an unformatted `$v` reference to a multi-value variable (`multi: true`, `includeAll: false`) holding `values`. `want` is what Grafana's own formatting function returned for it. `TestVariableInterpolatorMatchesGrafana` checks every case.

The file is data only. It contains no Grafana code.

## How the cases were produced

The upstream functions below were run in Node. Each datasource got the same 60 hand-picked cases and 260 cases from a seeded PRNG (mulberry32, seed 20261031).
- **Hand-picked cases:** every special character alone and inside `a…b`, `O'Brien`, `C:\tmp`, backslash runs, `$1`, `${var}`, empty values, and non-ASCII (`é`, `naïve`, `日本`).
- **Generated cases:** 1 to 4 values of 1 to 6 characters, drawn from `abcxyzABZ019`, space and the specials ``$ ^ * { } [ ] + ? . ( ) | \ ' " - _ / :``. The specials are weighted double, and 6% of values are empty.

The commits are pinned, and the `source` field in `golden.json` repeats these links:

- Prometheus `interpolateQueryExpr` and its escapes, with `prometheusSpecialCharsInLabelValues` off (the default): [grafana-prometheus-datasource@3bfc304 escaping.ts](https://github.com/grafana/grafana-prometheus-datasource/blob/3bfc304ade246040662915c46662c3d4352690b3/packages/grafana-prometheus/src/escaping.ts#L7-L69)
- Loki `interpolateQueryExpr`: [grafana-loki-datasource@323b451 datasource.ts](https://github.com/grafana/grafana-loki-datasource/blob/323b451414e36ac7277eadff2bacf548795862d1/src/datasource.ts#L873-L885) and [`lokiSpecialRegexEscape`](https://github.com/grafana/grafana-loki-datasource/blob/323b451414e36ac7277eadff2bacf548795862d1/src/datasource.ts#L1274-L1279)
- MySQL and MSSQL: the shared [`interpolateVariable`](https://github.com/grafana/grafana/blob/a7c265f91c48d83f97d5849c4e4842983d8ab535/packages/grafana-sql/src/datasource/SqlDatasource.ts#L77-L96) with each plugin's `quoteLiteral` ([mysql@b646347](https://github.com/grafana/grafana-mysql-datasource/blob/b646347ffcc80dc730500303c933634bd9c9334d/src/sqlUtil.ts#L69-L71), [mssql@4dd6dba](https://github.com/grafana/grafana-mssql-datasource/blob/4dd6dba5cfcd5ec2c1f8a89c2092a8c11b7554b4/src/MSSqlQueryModel.ts#L16-L18))
- PostgreSQL: its own [`interpolateVariable`](https://github.com/grafana/grafana-postgresql-datasource/blob/091b84dd8925c33777bdb8a8a97c7753085d77dd/src/datasource.ts#L37-L57) and [`quoteLiteral`](https://github.com/grafana/grafana-postgresql-datasource/blob/091b84dd8925c33777bdb8a8a97c7753085d77dd/src/PostgresQueryModel.ts#L21-L23)

Several values were passed as an array. A single value was passed as a string. For PostgreSQL, whose override doesn't quote a string, `want_if_array` gives the result for a one-element array instead; it is present only where the two differ (97 cases). Grafana passes the array: once a multi-value variable's options load, [scenes](https://github.com/grafana/scenes/blob/9bb68328651a14c2a57905cc4c3a6964895c426b/packages/scenes/src/variables/interpolation/sceneInterpolator.ts#L90-L119) holds its selection as an array, so the test uses `want_if_array` when it is set.

To regenerate, run those functions over the same inputs. The generator copies Grafana's AGPL-licensed functions, so it isn't committed here; it can be shared on request.
