import { Component, type ErrorInfo, type ReactNode } from 'react';

/**
 * Renders a failure instead of nothing.
 *
 * An uncaught render error unmounts the React tree, which in an MCP App host
 * means a blank panel: no message, no console the user can open, nothing to
 * distinguish a crash from "still loading". The host also cannot be attached to
 * a debugger (Claude Desktop refuses to start with a debugging switch), so the
 * app has to surface its own errors or they are unobservable.
 */

type Props = {
  children: ReactNode;
  /** Rendered instead of the default panel, if the consumer wants its own chrome. */
  fallback?: (error: Error) => ReactNode;
};

type State = { error?: Error };

export class AppErrorBoundary extends Component<Props, State> {
  state: State = {};

  static getDerivedStateFromError(error: Error): State {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    // Goes to the host's console when there is one, and is visible in the panel
    // either way.
    console.error('MCP app render failed', error, info.componentStack);
  }

  render() {
    const { error } = this.state;
    if (!error) return this.props.children;
    if (this.props.fallback) return this.props.fallback(error);

    return (
      <div
        style={{
          font: '13px system-ui, sans-serif',
          padding: 16,
          color: '#ce354d',
          background: '#fff5f5',
          border: '1px solid #ff7384',
          borderRadius: 6,
        }}
      >
        <strong>This visualization failed to render.</strong>
        <div style={{ marginTop: 8, font: '12px ui-monospace, SFMono-Regular, monospace', whiteSpace: 'pre-wrap' }}>
          {error.message}
          {error.stack ? `\n\n${error.stack.split('\n').slice(0, 6).join('\n')}` : ''}
        </div>
      </div>
    );
  }
}

/**
 * Capture errors thrown outside React — module evaluation, async callbacks,
 * rejected promises — so they are visible in the panel too.
 */
export function installGlobalErrorReporter(mount: HTMLElement): void {
  const seen: string[] = [];

  const report = (label: string, detail: unknown) => {
    const text = detail instanceof Error ? `${detail.message}\n${detail.stack ?? ''}` : String(detail);
    seen.push(`${label}: ${text}`);
    let panel = document.getElementById('mcp-app-global-error');
    if (!panel) {
      panel = document.createElement('pre');
      panel.id = 'mcp-app-global-error';
      panel.style.cssText =
        'font:12px ui-monospace,SFMono-Regular,monospace;white-space:pre-wrap;padding:12px;margin:0;' +
        'color:#ce354d;background:#fff5f5;border:1px solid #ff7384;border-radius:6px';
      mount.prepend(panel);
    }
    panel.textContent = seen.slice(0, 5).join('\n\n');
  };

  window.addEventListener('error', (event) => report('error', event.error ?? event.message));
  window.addEventListener('unhandledrejection', (event) => report('unhandled rejection', event.reason));
}
