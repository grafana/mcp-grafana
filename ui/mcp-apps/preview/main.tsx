import { Button } from '../src/design';
import { useState } from 'react';
import { createRoot } from 'react-dom/client';
import { McpAppSection, McpAppShell } from '../src';
import '../styles.css';
import { getStyles } from './Preview.styles';

type PreviewState = 'ready' | 'pending' | 'success' | 'error';

function Example({ mode, longContent }: { mode: 'light' | 'dark'; longContent: boolean }) {
  const styles = getStyles();
  const [state, setState] = useState<PreviewState>('ready');
  const [windowMinutes, setWindowMinutes] = useState(15);
  const [copied, setCopied] = useState(false);

  async function apply() {
    setState('pending');
    await new Promise((resolve) => setTimeout(resolve, 800));
    setState('success');
  }

  return (
    <section className={styles.example} aria-label={`${mode} mode preview`}>
      <h2 className={styles.label}>{mode === 'light' ? 'Light' : 'Dark'} mode</h2>
      <McpAppShell
        colorMode={mode}
        product="Grafana"
        scope={
          longContent
            ? 'production/europe-west/checkout-service-with-a-very-long-entity-name'
            : 'namespace/checkoutservice'
        }
        openInGrafana={{ href: 'https://grafana.com/' }}
        share={
          <Button
            variant="ghost"
            size="sm"
            onClick={async () => {
              try {
                await navigator.clipboard.writeText(window.location.href);
                setCopied(true);
              } catch {
                setState('error');
              }
            }}
          >
            {copied ? 'Link copied' : 'Copy preview link'}
          </Button>
        }
        summary={{
          label: 'Summary of proposal',
          title: longContent
            ? 'Review checkout latency across all production regions before applying the proposed configuration'
            : 'Review checkout latency',
          description: 'The summary gives the user enough context to understand the proposal before taking action.',
        }}
        primaryAction={
          state === 'success'
            ? undefined
            : {
                label: 'Apply preview',
                onClick: () => {
                  void apply();
                },
                pending: state === 'pending',
              }
        }
        secondaryAction={{
          label: state === 'success' ? 'Reset preview' : 'Preview error',
          onClick: () => setState(state === 'success' ? 'ready' : 'error'),
          disabled: state === 'pending',
        }}
        feedback={
          state === 'success'
            ? {
                tone: 'success',
                message: 'Preview applied. No Grafana configuration was changed.',
                action: { label: 'Reset', onClick: () => setState('ready') },
              }
            : state === 'error'
              ? {
                  tone: 'error',
                  message: 'Could not complete the preview action. Your selection is preserved.',
                  action: {
                    label: 'Try again',
                    onClick: () => {
                      void apply();
                    },
                  },
                }
              : undefined
        }
        tip={{
          message: 'Related app suggestions are optional.',
          action: { label: 'Show success state', onClick: () => setState('success') },
        }}
      >
        <McpAppSection
          title="Interactive surface"
          description="App-specific visualizations and controls go here."
          result={`Selected window: ${windowMinutes} minutes`}
        >
          <div className={styles.surface}>
            <span>Preview time window</span>
            <Button size="sm" onClick={() => setWindowMinutes(windowMinutes === 15 ? 60 : 15)}>
              {windowMinutes === 15 ? 'Use 60 minutes' : 'Use 15 minutes'}
            </Button>
          </div>
        </McpAppSection>
      </McpAppShell>
    </section>
  );
}

function Preview() {
  const styles = getStyles();
  const [mode, setMode] = useState<'both' | 'light' | 'dark'>('both');
  const [longContent, setLongContent] = useState(false);
  return (
    <main className={styles.page} data-color-mode="light">
      <h1 className={styles.heading}>Grafana MCP Apps shell</h1>
      <p className={styles.description}>Shared chrome, independent app content. Interactive component preview.</p>
      <div className={styles.toolbar} role="group" aria-label="Preview options">
        {(['both', 'light', 'dark'] as const).map((value) => (
          <Button key={value} size="sm" aria-pressed={mode === value} onClick={() => setMode(value)}>
            {value === 'both' ? 'Both themes' : value === 'light' ? 'Light theme' : 'Dark theme'}
          </Button>
        ))}
        <Button size="sm" aria-pressed={longContent} onClick={() => setLongContent(!longContent)}>
          Long content
        </Button>
      </div>
      <div className={styles.examples}>
        {mode !== 'dark' && <Example mode="light" longContent={longContent} />}
        {mode !== 'light' && <Example mode="dark" longContent={longContent} />}
      </div>
      <p className={styles.note}>
        Preview actions only update this page. Real MCP apps supply their own tool calls and host navigation.
      </p>
    </main>
  );
}

const root = document.getElementById('root');
if (root) {
  createRoot(root).render(<Preview />);
}
