import { css, cx } from '@emotion/css';
import type { ButtonHTMLAttributes, CSSProperties, ReactElement, ReactNode } from 'react';

/** Public, local values for the standalone MCP resource. Semantic colors stay scoped to each app root. */
export const getDesignTokens = () => ({
  primitives: {
    colors: {
      brandOrange: { 400: '#ff9830', 700: '#ad5100' },
      neutral: { 50: '#fafafa', 100: '#f4f5f5', 500: '#86898c', 700: '#404448' },
      smoke: { 800: '#24272b', 900: '#1b1d20' },
      gray: { 200: '#d9dce0', 400: '#92979e' },
      green: { 300: '#79d9a0', 400: '#56c785', 500: '#299c56', 600: '#208549', 700: '#176a3a' },
      orange: { 300: '#ffc47b', 400: '#ffa64d', 500: '#ed861d', 700: '#a95a0a' },
      red: { 400: '#ff7384', 500: '#e75065', 600: '#ce354d' },
      yellow: { 400: '#f4d35b', 700: '#ad8112' },
      teal: { 400: '#59bdad', 500: '#319c8d' },
    },
    spacing: { 1: '4px', 1.5: '6px', 2: '8px', 2.5: '10px', 3: '12px', 4: '16px', 5: '20px', 6: '24px', 7: '28px', 9: '36px', 12: '48px', 20: '80px' },
    borderRadius: { xs: '2px', sm: '4px', md: '6px' },
    borderWidth: { thin: '1px', medium: '2px', thick: '3px' },
    typography: {
      fontFamily: { ui: 'Inter, system-ui, sans-serif', monospace: 'ui-monospace, SFMono-Regular, monospace' },
      fontSize: { ui: { xs: '11px', sm: '12px', md: '14px', lg: '18px' }, monospace: { sm: '12px' } },
      fontWeight: { normal: 400, medium: 500, semibold: 600 },
    },
  },
  semantic: { colors: {
    surface: { background: 'var(--mcp-background)', foreground: 'var(--mcp-foreground)', cardForeground: 'var(--mcp-foreground)', inset: 'var(--mcp-inset)', insetForeground: 'var(--mcp-foreground)' },
    line: { border: 'var(--mcp-border)', input: 'var(--mcp-input-border)', ring: 'var(--mcp-ring)' },
    interactive: { muted: 'var(--mcp-muted)', mutedForeground: 'var(--mcp-muted-foreground)', mutedForegroundSubtle: 'var(--mcp-muted-foreground)', accent: 'var(--mcp-accent)', primary: 'var(--mcp-primary)' },
    status: { success: 'var(--mcp-success)', successSubtleForeground: 'var(--mcp-success-text)', destructive: 'var(--mcp-error)', destructiveSubtleForeground: 'var(--mcp-error-text)', info: 'var(--mcp-info)', infoSubtleForeground: 'var(--mcp-info-text)' },
  } },
});

export type CSSVariablesByColorMode = { light: Record<string, string>; dark: Record<string, string> };

export function GlobalCSSVariables({ variables }: { variables: CSSVariablesByColorMode; defaultColorMode?: 'light' | 'dark' }) {
  const rules = (mode: 'light' | 'dark') => `[data-color-mode="${mode}"] { ${Object.entries(variables[mode]).map(([key, value]) => `--${key}: ${value};`).join(' ')} }`;
  return <style>{`${rules('light')} ${rules('dark')}`}</style>;
}

const buttonClass = css({
  display: 'inline-flex', alignItems: 'center', justifyContent: 'center', gap: 6,
  border: '1px solid transparent', borderRadius: 6, fontFamily: 'inherit', fontWeight: 500,
  lineHeight: 1.2, whiteSpace: 'nowrap', textDecoration: 'none', cursor: 'pointer',
  '&:focus-visible': { outline: '2px solid var(--mcp-ring)', outlineOffset: 2 },
  '&:disabled, &[aria-disabled="true"]': { cursor: 'not-allowed', opacity: 0.55 },
});
const buttonVariants = {
  default: css({ background: 'var(--mcp-primary)', color: 'var(--mcp-primary-text)', '&:hover:not(:disabled)': { background: 'var(--mcp-primary-hover)' } }),
  secondary: css({ background: 'var(--mcp-muted)', color: 'var(--mcp-foreground)', borderColor: 'var(--mcp-border)', '&:hover:not(:disabled)': { background: 'var(--mcp-accent)' } }),
  ghost: css({ background: 'transparent', color: 'var(--mcp-foreground)', '&:hover:not(:disabled)': { background: 'var(--mcp-muted)' } }),
};
const buttonSizes = { xs: css({ minHeight: 24, padding: '3px 8px', fontSize: 11 }), sm: css({ minHeight: 30, padding: '5px 10px', fontSize: 12 }), default: css({ minHeight: 36, padding: '8px 12px', fontSize: 14 }) };

type ButtonProps = ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: keyof typeof buttonVariants;
  size?: keyof typeof buttonSizes;
  render?: ReactElement<{ href?: string; onClick?: React.MouseEventHandler<HTMLAnchorElement> }>;
  nativeButton?: boolean;
  children?: ReactNode;
};
export function Button({ variant = 'default', size = 'default', render, nativeButton: _nativeButton, className, children, ...props }: ButtonProps) {
  const classNames = cx(buttonClass, buttonVariants[variant], buttonSizes[size], className);
  if (render) {
    const { href, onClick } = render.props;
    return <a href={href} onClick={onClick} className={classNames} role={props.role}>{children}</a>;
  }
  return <button {...props} className={classNames}>{children}</button>;
}

const spinner = css({ width: 16, height: 16, border: '2px solid currentColor', borderRightColor: 'transparent', borderRadius: '50%', display: 'inline-block', animation: 'mcp-spin 800ms linear infinite', '@media (prefers-reduced-motion: reduce)': { animation: 'none', borderRightColor: 'currentColor' } });
export function LoadingIndicator({ size = 'sm' }: { size?: 'xs' | 'sm' }) {
  return <span className={spinner} style={{ width: size === 'xs' ? 12 : 16, height: size === 'xs' ? 12 : 16 }} role="status" aria-label="Loading" />;
}
export type PillColors = { bg: string; bgHover: string; text: string; textHover: string; focusOutline: string };
export function Pill({ label, colors }: { label: string; size?: 'small'; colors: PillColors }) {
  const style = { backgroundColor: colors.bg, color: colors.text } satisfies CSSProperties;
  return <span className={css({ display: 'inline-flex', alignItems: 'center', borderRadius: 999, padding: '2px 8px', fontSize: 11, fontWeight: 600, lineHeight: '16px' })} style={style}>{label}</span>;
}
