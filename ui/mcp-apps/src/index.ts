// The shell entry point of the package. It must not import ECharts or a visualization component: the library build
// fails if it does (entryPoints.config.ts). The other exports are in experimental.ts.
export { McpAppSection } from './McpAppSection';
export type { McpAppSectionProps } from './McpAppSection';
export { McpAppShell } from './McpAppShell';
export type {
  McpAppAction,
  McpAppColorMode,
  McpAppFeedback,
  McpAppOpenInGrafana,
  McpAppShellProps,
  McpAppSummary,
  McpAppTip,
} from './McpAppShell';
