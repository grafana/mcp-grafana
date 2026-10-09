import { formatValue, type Unit } from './format';
import { commonMetricName, seriesName, shortSeriesNames, type MetricSeries } from './types';
import { getVizStyles } from './Viz.styles';

/**
 * Tabular view of a query result. Always offered, whatever the response shape —
 * it is the one view that can represent any of them, and the fallback when a
 * chart hides detail the reader wants.
 *
 * Deliberately free of any chart dependency: a table is DOM.
 *
 * Layout follows Grafana's: a range query becomes Time plus one column per
 * series; an instant query becomes one row per series, with a column for each
 * label that distinguishes them, plus Value.
 */

export type TableProps = {
  series: MetricSeries[];
  unit?: Unit;
  decimals?: number;
  /** Rows drawn before truncating. The rest are summarised in the footer. */
  maxRows?: number;
};

const TIME_FORMAT = new Intl.DateTimeFormat(undefined, {
  hour: '2-digit',
  minute: '2-digit',
  second: '2-digit',
});

/** Label keys that differ across series — the ones worth a column. */
function distinguishingKeys(series: MetricSeries[]): string[] {
  const sets = series.map((s) => s.labels ?? {});
  const keys = new Set<string>();
  for (const labels of sets) for (const key of Object.keys(labels)) keys.add(key);
  return [...keys]
    .filter((key) => new Set(sets.map((labels) => labels[key])).size > 1)
    .sort();
}

export function Table({ series, unit = 'none', decimals, maxRows = 100 }: TableProps) {
  const styles = getVizStyles();
  const isRange = series.some((s) => s.points.length > 1);

  if (!series.length) {
    return <div className={styles.tableEmpty}>No rows.</div>;
  }

  return isRange ? rangeTable() : instantTable();

  /** Time down the side, one column per series. */
  function rangeTable() {
    const names = shortSeriesNames(series);
    // Series normally share a step, so the union of timestamps is the row set.
    const stamps = [...new Set(series.flatMap((s) => s.points.map(([at]) => at)))].sort((a, b) => a - b);
    const byStamp = series.map((s) => new Map(s.points));
    const shown = stamps.slice(0, maxRows);

    return (
      <div className={styles.tableScroll}>
        <table className={styles.table}>
          <thead>
            <tr>
              <th scope="col">Time</th>
              {names.map((name) => (
                <th scope="col" key={name} className={styles.tableNumeric}>
                  {name}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {shown.map((at) => (
              <tr key={at}>
                <th scope="row" className={styles.tableTime}>
                  {TIME_FORMAT.format(new Date(at))}
                </th>
                {byStamp.map((points, index) => {
                  const value = points.get(at);
                  return (
                    <td key={names[index]} className={styles.tableNumeric}>
                      {value === undefined ? '—' : formatValue(value, unit, decimals).formatted}
                    </td>
                  );
                })}
              </tr>
            ))}
          </tbody>
        </table>
        {stamps.length > shown.length && (
          <div className={styles.tableFooter}>
            Showing the first {shown.length} of {stamps.length} timestamps.
          </div>
        )}
      </div>
    );
  }

  /** One row per series: its distinguishing labels, then the value. */
  function instantTable() {
    const keys = distinguishingKeys(series);
    const metric = commonMetricName(series);
    const shown = series.slice(0, maxRows);

    return (
      <div className={styles.tableScroll}>
        <table className={styles.table}>
          <thead>
            <tr>
              {keys.length ? (
                keys.map((key) => (
                  <th scope="col" key={key}>
                    {key}
                  </th>
                ))
              ) : (
                <th scope="col">{metric ? 'Metric' : 'Series'}</th>
              )}
              <th scope="col" className={styles.tableNumeric}>
                Value
              </th>
            </tr>
          </thead>
          <tbody>
            {shown.map((s, index) => {
              const points = s.points;
              const value = points[points.length - 1]?.[1];
              return (
                <tr key={seriesName(s.labels) || index}>
                  {keys.length ? (
                    keys.map((key) => (
                      <td key={key} className={styles.tableLabel}>
                        {s.labels?.[key] ?? '—'}
                      </td>
                    ))
                  ) : (
                    <th scope="row" className={styles.tableLabel}>
                      {metric ?? seriesName(s.labels)}
                    </th>
                  )}
                  <td className={styles.tableNumeric}>
                    {value === undefined ? '—' : formatValue(value, unit, decimals).formatted}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
        {series.length > shown.length && (
          <div className={styles.tableFooter}>
            Showing the first {shown.length} of {series.length} series.
          </div>
        )}
      </div>
    );
  }
}
