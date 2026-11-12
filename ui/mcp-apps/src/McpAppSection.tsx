import { type ReactNode, useId } from 'react';
import { GlobalCSSVariables } from './design';

import { getMcpAppSectionStyles, mcpAppSectionCSSVariables } from './McpAppSection.styles';

export interface McpAppSectionProps {
  title: ReactNode;
  description?: ReactNode;
  actions?: ReactNode;
  children: ReactNode;
  result?: ReactNode;
}

export function McpAppSection({ title, description, actions, children, result }: McpAppSectionProps) {
  const styles = getMcpAppSectionStyles();
  const titleId = `mcp-app-section-${useId()}`;
  const hasResult = result !== undefined && result !== null;

  return (
    <>
      <GlobalCSSVariables variables={mcpAppSectionCSSVariables} defaultColorMode="light" />
      <section className={styles.section} aria-labelledby={titleId}>
        <div className={styles.headingRow}>
          <div className={styles.headingGroup}>
            <h2 id={titleId} className={styles.title}>
              {title}
            </h2>
            {description !== undefined && description !== null && (
              <div className={styles.description}>{description}</div>
            )}
          </div>
          {actions}
        </div>
        <div className={styles.body}>{children}</div>
        {hasResult && <div className={styles.result}>{result}</div>}
      </section>
    </>
  );
}
