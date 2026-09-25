import type { RenderServiceHealthResult } from '../src/service-health/types';

const object = (value: unknown): value is Record<string, unknown> =>
  value !== null && typeof value === 'object' && !Array.isArray(value);
const finite = (value: unknown): value is number => typeof value === 'number' && Number.isFinite(value);
const nullableString = (value: unknown) => value === null || typeof value === 'string';
const nonnegative = (value: unknown) => value === null || (finite(value) && value >= 0);
const validURL = (value: unknown) => {
  if (value === null) {
    return true;
  }
  if (typeof value !== 'string') {
    return false;
  }
  try {
    const url = new URL(value);
    return ['http:', 'https:'].includes(url.protocol) && !url.username && !url.password;
  } catch {
    return false;
  }
};

/** Validate untrusted tool output before displaying metrics or following links. */
export function parseServiceHealthResult(value: unknown): RenderServiceHealthResult | undefined {
  if (
    !object(value) ||
    typeof value.serviceName !== 'string' ||
    !value.serviceName ||
    typeof value.datasourceUid !== 'string' ||
    !value.datasourceUid ||
    !nullableString(value.serviceNamespace) ||
    !finite(value.fromMs) ||
    !finite(value.toMs) ||
    value.fromMs >= value.toMs ||
    !['1h', '6h', '24h', '7d'].includes(String(value.timeRange)) ||
    !['healthy', 'degraded', 'unknown'].includes(String(value.status)) ||
    typeof value.summary !== 'string' ||
    !nullableString(value.description) ||
    !validURL(value.grafanaUrl) ||
    !validURL(value.dashboardUrl) ||
    !validURL(value.sloUrl) ||
    !object(value.metrics) ||
    !Array.isArray(value.dependencies) ||
    value.dependencies.length > 100 ||
    !Array.isArray(value.warnings) ||
    !value.warnings.every((warning) => typeof warning === 'string')
  ) {
    return undefined;
  }
  for (const [key, unit] of [
    ['latencyP95', 'ms'],
    ['errorRatio', 'ratio'],
    ['requestRate', 'requests_per_second'],
  ]) {
    const metric = value.metrics[key];
    if (
      !object(metric) ||
      metric.unit !== unit ||
      !nonnegative(metric.value) ||
      (unit === 'ratio' && finite(metric.value) && metric.value > 1) ||
      !nullableString(metric.unavailableReason) ||
      !Array.isArray(metric.points) ||
      metric.points.length > 1500
    ) {
      return undefined;
    }
    let previous = -Infinity;
    for (const point of metric.points) {
      if (
        !object(point) ||
        !finite(point.timestampMs) ||
        point.timestampMs < previous ||
        point.timestampMs < value.fromMs ||
        point.timestampMs > value.toMs ||
        !nonnegative(point.value) ||
        (unit === 'ratio' && finite(point.value) && point.value > 1)
      ) {
        return undefined;
      }
      previous = point.timestampMs;
    }
  }
  for (const dependency of value.dependencies) {
    if (
      !object(dependency) ||
      typeof dependency.name !== 'string' ||
      !dependency.name ||
      !nullableString(dependency.namespace) ||
      !['service', 'database', 'unknown'].includes(String(dependency.type)) ||
      !nonnegative(dependency.latencyP95Ms) ||
      !nonnegative(dependency.errorRatio) ||
      (finite(dependency.errorRatio) && dependency.errorRatio > 1) ||
      !nullableString(dependency.unavailableReason)
    ) {
      return undefined;
    }
  }
  return value as unknown as RenderServiceHealthResult;
}
