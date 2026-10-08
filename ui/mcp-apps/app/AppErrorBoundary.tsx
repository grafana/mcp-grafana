import { Component, type ErrorInfo, type ReactNode } from 'react';

import { McpAppShell, type McpAppColorMode } from '../src/McpAppShell';

/**
 * Renders a failure instead of nothing.
 *
 * An uncaught render error unmounts the React tree, which in an MCP App host
 * means a blank panel: no message, and no console the user can open, so the
 * app has to surface its own errors.
 */

type Props = {
  children: ReactNode;
  colorMode: McpAppColorMode;
  label: string;
};

type State = { error?: Error };

export class AppErrorBoundary extends Component<Props, State> {
  state: State = {};

  static getDerivedStateFromError(error: Error): State {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error('MCP app render failed', error, info.componentStack);
  }

  render() {
    const { error } = this.state;
    if (!error) return this.props.children;

    return (
      <McpAppShell
        product="Grafana"
        colorMode={this.props.colorMode}
        summary={{ label: this.props.label, title: 'This view failed to render' }}
        feedback={{ tone: 'error', message: error.message }}
      />
    );
  }
}
