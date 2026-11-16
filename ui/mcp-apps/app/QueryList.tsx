import { css } from '@emotion/css';

import { getDesignTokens } from '../src/design';
import type { VizArgs } from './vizSpec';

type Query = VizArgs['queries'][number];

// The query text of the common data sources: Prometheus and Loki, SQL, and others.
const QUERY_TEXT_FIELDS = ['expr', 'rawSql', 'query', 'queryText'];

/** The text of a query, or undefined if it has none. Its line breaks stay. */
export function queryText(query: Query | undefined): string | undefined {
  return QUERY_TEXT_FIELDS.map((name) => query?.[name]).find(
    (value): value is string => typeof value === 'string' && value.trim() !== '',
  );
}

/**
 * The queries of the result under the chart, closed at first (#1585). A query with no text shows its model as JSON,
 * without refId and datasource. The queries are not editable here: the user changes them in the chat or in Explore.
 */
export function QueryList({ queries }: { queries: VizArgs['queries'] }) {
  return (
    <details className={styles.details}>
      <summary className={styles.summary}>{queries.length === 1 ? 'Query' : `Queries (${queries.length})`}</summary>
      <dl className={styles.list}>
        {queries.map((query) => {
          const { refId, datasource: _datasource, ...model } = query;
          return (
            <div key={refId} className={styles.item}>
              <dt className={styles.refId}>{refId}</dt>
              <dd className={styles.text}>
                <pre className={styles.pre}>{queryText(query)?.trim() ?? JSON.stringify(model, null, 2)}</pre>
              </dd>
            </div>
          );
        })}
      </dl>
    </details>
  );
}

// The tokens do not change, so the classes are made once.
const styles = (() => {
  const {
    primitives: { borderRadius, spacing, typography },
    semantic: { colors },
  } = getDesignTokens();
  return {
    details: css({ fontSize: typography.fontSize.ui.sm, color: colors.interactive.mutedForeground }),
    summary: css({ cursor: 'pointer', width: 'fit-content' }),
    list: css({ display: 'flex', flexDirection: 'column', gap: spacing[2], margin: `${spacing[2]} 0 0` }),
    item: css({ display: 'flex', gap: spacing[2], minWidth: 0 }),
    refId: css({ flex: 'none', paddingTop: spacing[1], fontWeight: typography.fontWeight.medium }),
    text: css({ margin: 0, minWidth: 0, flex: 1 }),
    pre: css({
      margin: 0,
      padding: `${spacing[1]} ${spacing[2]}`,
      borderRadius: borderRadius.sm,
      backgroundColor: colors.surface.inset,
      color: colors.surface.insetForeground,
      fontFamily: typography.fontFamily.monospace,
      fontSize: typography.fontSize.monospace.sm,
      whiteSpace: 'pre-wrap',
      overflowWrap: 'anywhere',
    }),
  };
})();
