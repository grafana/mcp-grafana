import { cx } from '@emotion/css';
import { Button } from './design';
import { LoadingIndicator } from './design';
import { GlobalCSSVariables } from './design';
import { CircleAlert, CircleCheck, ExternalLink, Info } from 'lucide-react';
import { type MouseEventHandler, type ReactNode, useId } from 'react';

import grafanaLogo from './grafana.svg';
import { getMcpAppShellStyles, mcpAppShellCSSVariables } from './McpAppShell.styles';

export type McpAppColorMode = 'light' | 'dark';

export interface McpAppAction {
  label: string;
  onClick: () => void;
  disabled?: boolean;
  pending?: boolean;
}

export interface McpAppSummary {
  label: string;
  status?: ReactNode;
  title: ReactNode;
  description?: ReactNode;
}

export interface McpAppOpenInGrafana {
  href: string;
  onClick?: MouseEventHandler<HTMLAnchorElement>;
}

export interface McpAppFeedback {
  tone: 'success' | 'error' | 'info';
  message: ReactNode;
  action?: McpAppAction;
}

export interface McpAppTip {
  message: ReactNode;
  action?: McpAppAction;
}

export interface McpAppShellProps {
  product: string;
  layout?: 'standard' | 'wide';
  density?: 'standard' | 'compact';
  scope?: string;
  /**
   * Sets the color mode on this shell only, so light and dark shells can coexist.
   * The app entry point must resolve the host theme or system preference.
   */
  colorMode: McpAppColorMode;
  children?: ReactNode;
  summary: McpAppSummary;
  primaryAction?: McpAppAction;
  secondaryAction?: McpAppAction;
  openInGrafana?: McpAppOpenInGrafana;
  /** A consumer-owned menu or trigger. The shell does not create a placeholder control. */
  share?: ReactNode;
  feedback?: McpAppFeedback;
  tip?: McpAppTip;
}

type ActionButtonProps = {
  action: McpAppAction;
  variant: 'default' | 'secondary' | 'ghost';
  size?: 'xs' | 'sm' | 'default';
};

function ActionButton({ action, variant, size = 'sm' }: ActionButtonProps) {
  const styles = getMcpAppShellStyles();
  const isDisabled = Boolean(action.disabled || action.pending);

  return (
    <Button
      type="button"
      variant={variant}
      size={size}
      onClick={action.onClick}
      disabled={isDisabled}
      aria-busy={action.pending || undefined}
    >
      {action.pending && (
        <span className={styles.loadingIndicator} aria-hidden="true">
          <LoadingIndicator size="xs" />
        </span>
      )}
      {action.label}
    </Button>
  );
}

const feedbackIcon = {
  success: CircleCheck,
  error: CircleAlert,
  info: Info,
} as const;

export function McpAppShell({
  product,
  layout = 'standard',
  density = 'standard',
  scope,
  colorMode,
  children,
  summary,
  primaryAction,
  secondaryAction,
  openInGrafana,
  share,
  feedback,
  tip,
}: McpAppShellProps) {
  const styles = getMcpAppShellStyles();
  const titleId = `mcp-app-summary-${useId()}`;
  const hasHeaderActions = Boolean(openInGrafana || share);
  const hasFooter = Boolean(feedback || tip || primaryAction || secondaryAction);

  return (
    <>
      <GlobalCSSVariables variables={mcpAppShellCSSVariables} defaultColorMode="light" />
      <article
        className={cx(styles.shell, layout === 'wide' && styles.wide, density === 'compact' && styles.compact)}
        data-color-mode={colorMode}
        aria-labelledby={titleId}
      >
        <header className={styles.header}>
          <div className={styles.identity}>
            <img className={styles.logo} src={grafanaLogo} width={16} height={16} alt="" aria-hidden="true" />
            <div className={styles.identityText}>
              <span className={styles.product}>{product}</span>
              {scope && (
                <>
                  <span className={styles.separator} aria-hidden="true">
                    ·
                  </span>
                  <span className={styles.scope}>{scope}</span>
                </>
              )}
            </div>
          </div>

          {hasHeaderActions && (
            <div className={cx(styles.headerActions, styles.narrowHeaderActions)}>
              {openInGrafana && (
                <Button
                  render={<a href={openInGrafana.href} onClick={openInGrafana.onClick} />}
                  nativeButton={false}
                  role="link"
                  variant="ghost"
                  size="xs"
                >
                  Open in Grafana
                  <ExternalLink size={12} aria-hidden="true" />
                </Button>
              )}
              {share}
            </div>
          )}
        </header>

        <div className={cx(styles.summary, density === 'compact' && styles.compactSummary)}>
          <div className={styles.summaryHeader}>
            <span className={styles.summaryLabel}>{summary.label}</span>
            {summary.status}
          </div>
          <h1 id={titleId} className={styles.summaryTitle}>
            {summary.title}
          </h1>
          {summary.description !== undefined && summary.description !== null && (
            <div className={styles.summaryDescription}>{summary.description}</div>
          )}
        </div>

        {children !== undefined && children !== null && <div className={styles.content}>{children}</div>}

        {hasFooter && (
          <footer className={styles.footer}>
            {(primaryAction || secondaryAction) && (
              <div className={cx(styles.actions, styles.narrowActions)}>
                {primaryAction && <ActionButton action={primaryAction} variant="default" />}
                {secondaryAction && <ActionButton action={secondaryAction} variant="secondary" />}
              </div>
            )}

            {feedback && (
              <div
                className={cx(
                  styles.feedback,
                  styles.narrowFeedback,
                  feedback.tone === 'success' && styles.feedbackSuccess,
                  feedback.tone === 'error' && styles.feedbackError,
                  feedback.tone === 'info' && styles.feedbackInfo
                )}
              >
                <span className={styles.feedbackIcon} aria-hidden="true">
                  {(() => { const FeedbackIcon = feedbackIcon[feedback.tone]; return <FeedbackIcon size={16} aria-hidden="true" />; })()}
                </span>
                <div
                  className={styles.feedbackMessage}
                  role={feedback.tone === 'error' ? 'alert' : 'status'}
                  aria-atomic="true"
                >
                  {feedback.message}
                </div>
                {feedback.action && (
                  <div className={cx(styles.feedbackAction, styles.narrowFeedbackAction)}>
                    <ActionButton action={feedback.action} variant="ghost" size="xs" />
                  </div>
                )}
              </div>
            )}

            {tip && (
              <aside className={cx(styles.tip, styles.narrowFeedback)}>
                <span className={styles.feedbackIcon} aria-hidden="true">
                  <Info size={16} aria-hidden="true" />
                </span>
                <div className={styles.feedbackMessage}>{tip.message}</div>
                {tip.action && (
                  <div className={cx(styles.feedbackAction, styles.narrowFeedbackAction)}>
                    <ActionButton action={tip.action} variant="ghost" size="xs" />
                  </div>
                )}
              </aside>
            )}
          </footer>
        )}
      </article>
    </>
  );
}
