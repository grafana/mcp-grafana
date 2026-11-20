import { cx } from '@emotion/css';
import { Check, ChevronDown, ChevronRight, CircleAlert, Copy, Database } from 'lucide-react';
import { useEffect, useId, useRef, useState } from 'react';

import { copyText } from './copyText';
import { Button } from './design';
import { getQueryBlockStyles } from './QueryBlock.styles';

export type QueryBlockQuery = { refId: string; text: string };

export type QueryBlockProps = {
  /** The data source of the queries, shown in a chip. */
  datasource: string;
  queries: QueryBlockQuery[];
};

/** How long Copy shows its result. */
export const COPY_RESULT_MS = 1500;

const COPY = {
  idle: { label: 'Copy query', Icon: Copy },
  copied: { label: 'Query copied', Icon: Check },
  failed: { label: 'Could not copy the query', Icon: CircleAlert },
} as const;

/**
 * The queries behind a chart. Collapsed, it shows them on one line; expanded, each query as written, with its refId
 * when there are several. Copy puts the text of all queries on the clipboard.
 */
export function QueryBlock({ datasource, queries }: QueryBlockProps) {
  const styles = getQueryBlockStyles();
  const [expanded, setExpanded] = useState(false);
  const [copy, setCopy] = useState<keyof typeof COPY>('idle');
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined);
  const bodyId = useId();
  useEffect(() => () => clearTimeout(timer.current), []);

  const onCopy = async () => {
    const copied = await copyText(queries.map((query) => query.text).join('\n\n'));
    setCopy(copied ? 'copied' : 'failed');
    clearTimeout(timer.current);
    timer.current = setTimeout(() => setCopy('idle'), COPY_RESULT_MS);
  };
  const { label: copyLabel, Icon: CopyIcon } = COPY[copy];
  const Chevron = expanded ? ChevronDown : ChevronRight;

  return (
    <section className={styles.block} aria-label="Query">
      <div className={styles.header}>
        <Button
          variant="ghost"
          size="xs"
          className={styles.iconButton}
          aria-label={expanded ? 'Collapse query' : 'Expand query'}
          aria-expanded={expanded}
          aria-controls={bodyId}
          onClick={() => setExpanded(!expanded)}
        >
          <Chevron size={14} aria-hidden="true" />
        </Button>
        <span className={styles.chip} title={datasource}>
          <Database size={12} aria-hidden="true" />
          <span className={styles.chipText}>{datasource}</span>
        </span>
        <Button
          variant="ghost"
          size="xs"
          className={cx(styles.iconButton, styles.copy)}
          aria-label={copyLabel}
          title={copyLabel}
          onClick={onCopy}
        >
          <CopyIcon size={14} aria-hidden="true" />
        </Button>
        {/* The label of Copy changes under focus, which a screen reader does not read out; this does. */}
        <span className={styles.srOnly} role="status">
          {copy === 'idle' ? '' : copyLabel}
        </span>
      </div>

      {expanded ? (
        <div id={bodyId} className={styles.queries}>
          {/* By position: a refId can be missing or repeated until the server checks the queries. */}
          {queries.map(({ refId, text }, i) => (
            <div key={i}>
              {queries.length > 1 && refId && <div className={styles.refId}>{refId}</div>}
              <pre className={styles.code}>{text}</pre>
            </div>
          ))}
        </div>
      ) : (
        <div id={bodyId} className={styles.preview}>
          {queries.map((query) => oneLine(query.text)).join('  ·  ')}
        </div>
      )}
    </section>
  );
}

const oneLine = (text: string) => text.replace(/\s+/g, ' ').trim();
