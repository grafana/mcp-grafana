import { Button } from '../src/design';
import { useState } from 'react';
import { createRoot } from 'react-dom/client';
import { RenderTraceApp } from '../src/trace/RenderTraceApp';
import { trace } from './traceFixture';
import { getStyles } from './Preview.styles';
import '../styles.css';

function Preview() {
  const [dark, setDark] = useState(false);
  const [empty, setEmpty] = useState(false);
  const [notice, setNotice] = useState('');
  const styles = getStyles();
  return (
    <main className={styles.page} data-color-mode={dark ? 'dark' : 'light'}>
      <div className={styles.toolbar}>
        <Button onClick={() => setDark(!dark)}>{dark ? 'Light mode' : 'Dark mode'}</Button>
        <Button onClick={() => setEmpty(!empty)}>{empty ? 'Show large trace' : 'Show empty trace'}</Button>
        <span>Synthetic preview · 12,000 spans</span>
      </div>
      <RenderTraceApp
        result={empty ? { ...trace, spans: [] } : trace}
        colorMode={dark ? 'dark' : 'light'}
        share={
          <Button
            variant="ghost"
            size="xs"
            onClick={() => setNotice('Demo sharing: the deployed app copies the Grafana trace link.')}
          >
            Share
          </Button>
        }
        onOpenInGrafana={() => setNotice('Demo navigation: the deployed app opens this trace in Grafana Explore.')}
      />
      {notice && <p role="status">{notice}</p>}
    </main>
  );
}
createRoot(document.getElementById('root')!).render(<Preview />);
