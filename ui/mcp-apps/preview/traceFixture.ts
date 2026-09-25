import type { RenderTraceResult, TraceSpan } from '../src/trace/types';

export const trace: RenderTraceResult = {
  traceId: '4bf92f3577b34da6a3ce929d0e0e4736',
  datasourceUid: 'tempo-demo',
  focusSpanId: '0000000000000003',
  grafanaUrl: 'https://example.grafana.net/explore',
  spans: Array.from(
    { length: 12000 },
    (_, index): TraceSpan => ({
      id: index.toString(16).padStart(16, '0'),
      parentId: index === 0 ? undefined : (index < 4 ? index - 1 : 1).toString(16).padStart(16, '0'),
      name:
        index === 0
          ? 'POST /checkout/batch'
          : index === 1
            ? 'checkout batch'
            : index === 2
              ? 'process payment batch'
              : index === 3
                ? 'authorize payment'
                : `process item #${index}`,
      serviceName: [
        'frontend',
        'checkoutservice',
        'paymentservice',
        'paymentservice',
        'inventoryservice',
        'cartservice',
        'shippingservice',
        'taxservice',
      ][index % 8],
      startTimeMs:
        1720000000000 + (index < 2 ? index * 12 : index === 2 ? 18400 : index === 3 ? 19400 : (index * 137) % 31000),
      durationMs:
        index === 0
          ? 32800
          : index === 1
            ? 32600
            : index === 2
              ? 13700
              : index === 3
                ? 12000
                : 25 + ((index * 73) % 1700),
      status: index === 3 ? 'error' : 'ok',
      attributes: index === 3 ? { 'rpc.system': 'grpc', 'rpc.method': 'Authorize' } : {},
      events:
        index === 3
          ? [
              {
                name: 'exception',
                timeMs: 1720000031400,
                attributes: {
                  'exception.type': 'java.net.SocketTimeoutException',
                  'exception.message': 'Payment gateway did not respond within 12000 ms',
                  'exception.stacktrace':
                    'java.net.SocketTimeoutException: Read timed out\n    at sun.nio.ch.NioSocketImpl.timedRead(NioSocketImpl.java:288)\n    at com.example.payments.GatewayClient.authorize(GatewayClient.java:142)\n    at com.example.payments.PaymentService.authorize(PaymentService.java:87)',
                },
              },
            ]
          : [],
    })
  ),
};
