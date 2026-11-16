import { css } from '@emotion/css';
import { ChevronRight } from 'lucide-react';
import { getDesignTokens } from '../design';
import type { LogsViewResult } from './parseLogsResult';

type LogEntry = LogsViewResult['logs']['data'][number];

export function LogsView({ result }: { result: LogsViewResult }) {
  const entries = result.logs.data;
  const logLines = entries.filter((entry) => entry.value === undefined && entry.values === undefined);

  return (
    <section className={styles.view} aria-label="Log lines">
      {result.logs.metadata?.resultsTruncated && (
        <p role="status">The log limit was reached. Narrow the query or time range to inspect more logs.</p>
      )}
      {logLines.length === 0 ? (
        <p>
          {entries.length
            ? 'This query returned metric samples. Use a log query to show log lines.'
            : 'No logs matched this query.'}
        </p>
      ) : (
        <div className={styles.rows}>
          {logLines.map((entry, index) => (
            <LogRow key={`${entry.timestamp}-${index}`} entry={entry} />
          ))}
        </div>
      )}
    </section>
  );
}

function LogRow({ entry }: { entry: LogEntry }) {
  const level =
    entry.labels.detected_level ??
    entry.labels.level ??
    entry.parsed?.detected_level ??
    entry.parsed?.level ??
    entry.structuredMetadata?.detected_level ??
    entry.structuredMetadata?.level;
  const normalizedLevel = level?.toLowerCase();
  const levelColor =
    normalizedLevel && Object.hasOwn(levelColors, normalizedLevel) ? levelColors[normalizedLevel] : undefined;
  const groups = [
    { title: 'Indexed labels', fields: entry.labels },
    { title: 'Parsed fields', fields: entry.parsed },
    { title: 'Structured metadata', fields: entry.structuredMetadata },
  ].map(({ title, fields }) => ({
    title,
    fields: Object.entries(fields ?? {}),
  }));

  return (
    <details className={styles.row}>
      <summary className={styles.line} title="Expand or collapse log details">
        <ChevronRight className={styles.chevron} size={12} aria-hidden="true" />
        <time className={styles.timestamp} dateTime={entry.timestamp} title={entry.timestamp}>
          {entry.timestamp ?? '—'}
        </time>
        <span className={levelColor ?? styles.unknownLevel}>{level?.toUpperCase() ?? '—'}</span>
        <span className={styles.message}>{entry.line ?? ''}</span>
      </summary>
      <div className={styles.inspector}>
        {groups.map(
          ({ title, fields }) =>
            fields.length > 0 && (
              <details key={title} className={styles.fieldSection}>
                <summary>
                  {title} <span className={styles.fieldCount}>{fields.length}</span>
                </summary>
                <table className={styles.fields} aria-label={title}>
                  <tbody>
                    {fields.map(([key, value]) => (
                      <tr key={key}>
                        <th scope="row">{key}</th>
                        <td>{value}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </details>
            )
        )}
        {groups.every(({ fields }) => fields.length === 0) && (
          <p role="status">No fields available for this log line.</p>
        )}
      </div>
    </details>
  );
}

const {
  primitives: { typography, colors },
  semantic: { colors: theme },
} = getDesignTokens();
const levelColors: Record<string, string> = {
  info: css({ color: theme.status.info }),
  error: css({ color: theme.status.destructive }),
  fatal: css({ color: theme.status.destructive }),
  critical: css({ color: theme.status.destructive }),
  warn: css({ color: colors.orange[700], "[data-color-mode='dark'] &": { color: colors.orange[400] } }),
  warning: css({ color: colors.orange[700], "[data-color-mode='dark'] &": { color: colors.orange[400] } }),
};

const styles = {
  view: css({
    width: '100%',
    minWidth: 0,
    '& p': { margin: 0, padding: '8px 0', color: theme.interactive.mutedForeground },
  }),
  rows: css({ minWidth: 0, overflowX: 'auto' }),
  row: css({
    minWidth: 0,
    '&[open] > summary': { backgroundColor: theme.interactive.muted },
    '&[open] > summary > svg': { transform: 'rotate(90deg)' },
    '&[open] > summary > span:last-child': {
      height: 'auto',
      whiteSpace: 'pre-wrap',
      overflowWrap: 'anywhere',
    },
  }),
  line: css({
    display: 'grid',
    gridTemplateColumns: '12px 30ch 7ch minmax(0, 1fr)',
    alignItems: 'baseline',
    columnGap: 8,
    padding: '3px 6px',
    minWidth: 0,
    listStyle: 'none',
    cursor: 'pointer',
    fontFamily: typography.fontFamily.monospace,
    fontSize: 12,
    lineHeight: '20px',
    '&::-webkit-details-marker': { display: 'none' },
    '&:hover': { backgroundColor: theme.interactive.muted },
    '&:focus-visible': { outline: `2px solid ${theme.line.ring}`, outlineOffset: -2 },
    '& > span:not(:last-child)': { fontWeight: 600 },
    '@media (max-width: 540px)': {
      gridTemplateColumns: '12px 30ch 7ch minmax(180px, 1fr)',
      minWidth: 'max-content',
    },
  }),
  chevron: css({ alignSelf: 'center', color: theme.interactive.mutedForeground }),
  timestamp: css({ color: theme.interactive.mutedForeground, whiteSpace: 'nowrap' }),
  unknownLevel: css({ color: theme.interactive.mutedForeground }),
  message: css({
    display: 'block',
    overflow: 'hidden',
    textOverflow: 'ellipsis',
    whiteSpace: 'pre',
    height: 20,
    minWidth: 0,
  }),
  inspector: css({
    display: 'flex',
    flexDirection: 'column',
    gap: 8,
    padding: '12px 8px 12px 20px',
    borderLeft: `1px solid ${theme.line.border}`,
    margin: '4px 0 8px 12px',
  }),
  fieldSection: css({
    minWidth: 0,
    border: `1px solid ${theme.line.border}`,
    borderRadius: 4,
    '& > summary': { padding: '8px 12px', cursor: 'pointer', fontWeight: 600 },
    '& > summary:focus-visible': { outline: `2px solid ${theme.line.ring}`, outlineOffset: -2 },
  }),
  fieldCount: css({ color: theme.interactive.mutedForeground, fontWeight: 400, marginLeft: 6 }),
  fields: css({
    width: '100%',
    borderCollapse: 'collapse',
    tableLayout: 'fixed',
    fontSize: 12,
    '& th, & td': { padding: '5px 12px', verticalAlign: 'top', textAlign: 'left', overflowWrap: 'anywhere' },
    '& th': { width: '30%', fontWeight: 500, color: theme.interactive.mutedForeground },
    '& td': { fontFamily: typography.fontFamily.monospace, whiteSpace: 'pre-wrap' },
    '& tr:last-child th, & tr:last-child td': { paddingBottom: 12 },
  }),
};
