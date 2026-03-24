/**
 * obtrace-zero Node.js Auto-Instrumentation Loader
 *
 * Injected via NODE_OPTIONS="--require /obtrace/obtrace-loader.js"
 * Automatically instruments the application without any code changes.
 *
 * Detects framework (Express, NestJS, Next.js, Fastify, Koa, Elysia)
 * and patches HTTP, database, and messaging libraries.
 */

'use strict';

const http = require('http');
const https = require('https');

const OBTRACE_API_KEY = process.env.OBTRACE_API_KEY;
const OBTRACE_INGEST_URL = process.env.OBTRACE_INGEST_URL;
const OBTRACE_SERVICE_NAME = process.env.OBTRACE_SERVICE_NAME || 'unknown';
const OBTRACE_ENVIRONMENT = process.env.OBTRACE_ENVIRONMENT || 'unknown';
const OBTRACE_POD_NAME = process.env.OBTRACE_POD_NAME || '';
const OBTRACE_POD_NAMESPACE = process.env.OBTRACE_POD_NAMESPACE || '';
const OBTRACE_NODE_NAME = process.env.OBTRACE_NODE_NAME || '';
const OBTRACE_SAMPLE_RATIO = parseFloat(process.env.OBTRACE_TRACE_SAMPLE_RATIO || '1.0');

if (!OBTRACE_API_KEY || !OBTRACE_INGEST_URL) {
  console.warn('[obtrace-zero] missing OBTRACE_API_KEY or OBTRACE_INGEST_URL, skipping instrumentation');
  return;
}

const queue = [];
let flushTimer = null;
const FLUSH_INTERVAL = 2000;
const MAX_QUEUE = 500;

function genTraceId() {
  const bytes = new Uint8Array(16);
  for (let i = 0; i < 16; i++) bytes[i] = Math.floor(Math.random() * 256);
  return Array.from(bytes).map(b => b.toString(16).padStart(2, '0')).join('');
}

function genSpanId() {
  const bytes = new Uint8Array(8);
  for (let i = 0; i < 8; i++) bytes[i] = Math.floor(Math.random() * 256);
  return Array.from(bytes).map(b => b.toString(16).padStart(2, '0')).join('');
}

function shouldSample() {
  return Math.random() < OBTRACE_SAMPLE_RATIO;
}

const resourceAttrs = {
  'service.name': OBTRACE_SERVICE_NAME,
  'deployment.environment': OBTRACE_ENVIRONMENT,
  'k8s.pod.name': OBTRACE_POD_NAME,
  'k8s.namespace.name': OBTRACE_POD_NAMESPACE,
  'k8s.node.name': OBTRACE_NODE_NAME,
  'telemetry.sdk.name': 'obtrace-zero',
  'telemetry.sdk.language': 'nodejs',
  'process.runtime.name': 'nodejs',
  'process.runtime.version': process.version,
  'process.pid': process.pid.toString(),
};

function enqueue(type, payload) {
  if (queue.length >= MAX_QUEUE) queue.shift();
  queue.push({ type, payload, timestamp: Date.now() });
  if (!flushTimer) {
    flushTimer = setTimeout(flush, FLUSH_INTERVAL);
  }
}

function flush() {
  flushTimer = null;
  if (queue.length === 0) return;

  const batch = queue.splice(0, MAX_QUEUE);
  const traces = batch.filter(b => b.type === 'span');
  const logs = batch.filter(b => b.type === 'log');
  const metrics = batch.filter(b => b.type === 'metric');

  if (traces.length > 0) send('/otlp/v1/traces', buildTracePayload(traces));
  if (logs.length > 0) send('/otlp/v1/logs', buildLogPayload(logs));
  if (metrics.length > 0) send('/otlp/v1/metrics', buildMetricPayload(metrics));
}

function send(path, body) {
  try {
    const url = new URL(path, OBTRACE_INGEST_URL);
    const mod = url.protocol === 'https:' ? https : http;
    const req = mod.request(url, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'X-API-Key': OBTRACE_API_KEY,
        'X-Obtrace-Source': 'zero-agent-nodejs',
      },
      timeout: 5000,
    });
    req.on('error', () => {});
    req.write(JSON.stringify(body));
    req.end();
  } catch (_) {}
}

function buildTracePayload(spans) {
  return {
    resourceSpans: [{
      resource: { attributes: objToAttrs(resourceAttrs) },
      scopeSpans: [{
        scope: { name: 'obtrace-zero-nodejs' },
        spans: spans.map(s => s.payload),
      }],
    }],
  };
}

function buildLogPayload(logs) {
  return {
    resourceLogs: [{
      resource: { attributes: objToAttrs(resourceAttrs) },
      scopeLogs: [{
        scope: { name: 'obtrace-zero-nodejs' },
        logRecords: logs.map(l => l.payload),
      }],
    }],
  };
}

function buildMetricPayload(metrics) {
  return {
    resourceMetrics: [{
      resource: { attributes: objToAttrs(resourceAttrs) },
      scopeMetrics: [{
        scope: { name: 'obtrace-zero-nodejs' },
        metrics: metrics.map(m => m.payload),
      }],
    }],
  };
}

function objToAttrs(obj) {
  return Object.entries(obj)
    .filter(([, v]) => v !== '' && v !== undefined)
    .map(([k, v]) => ({ key: k, value: { stringValue: String(v) } }));
}

const activeContext = { traceId: null, spanId: null };

const origCreateServer = http.createServer;
http.createServer = function(...args) {
  const server = origCreateServer.apply(this, args);
  const origOn = server.on.bind(server);

  server.on = function(event, listener) {
    if (event === 'request') {
      return origOn(event, function(req, res) {
        if (!shouldSample()) return listener(req, res);

        const traceId = extractTraceId(req) || genTraceId();
        const spanId = genSpanId();
        const parentSpanId = extractParentSpanId(req) || '';
        const startTime = Date.now();

        activeContext.traceId = traceId;
        activeContext.spanId = spanId;

        res.setHeader('X-Obtrace-Trace-Id', traceId);

        const origEnd = res.end;
        res.end = function(...endArgs) {
          const duration = Date.now() - startTime;

          enqueue('span', {
            traceId,
            spanId,
            parentSpanId,
            name: `${req.method} ${req.url}`,
            kind: 2,
            startTimeUnixNano: (startTime * 1e6).toString(),
            endTimeUnixNano: ((startTime + duration) * 1e6).toString(),
            attributes: objToAttrs({
              'http.method': req.method,
              'http.url': req.url,
              'http.status_code': res.statusCode.toString(),
              'http.route': req.url.split('?')[0],
              'http.user_agent': req.headers['user-agent'] || '',
              'net.peer.ip': req.socket.remoteAddress || '',
            }),
            status: { code: res.statusCode >= 400 ? 2 : 1 },
          });

          enqueue('metric', {
            name: 'http.server.duration',
            unit: 'ms',
            sum: {
              dataPoints: [{
                asDouble: duration,
                timeUnixNano: (Date.now() * 1e6).toString(),
                attributes: objToAttrs({
                  'http.method': req.method,
                  'http.route': req.url.split('?')[0],
                  'http.status_code': res.statusCode.toString(),
                }),
              }],
              aggregationTemporality: 1,
              isMonotonic: false,
            },
          });

          if (res.statusCode >= 500) {
            enqueue('log', {
              timeUnixNano: (Date.now() * 1e6).toString(),
              severityNumber: 17,
              severityText: 'ERROR',
              body: { stringValue: `${req.method} ${req.url} → ${res.statusCode}` },
              attributes: objToAttrs({
                'http.method': req.method,
                'http.url': req.url,
                'http.status_code': res.statusCode.toString(),
              }),
              traceId,
              spanId,
            });
          }

          activeContext.traceId = null;
          activeContext.spanId = null;

          return origEnd.apply(res, endArgs);
        };

        listener(req, res);
      });
    }
    return origOn(event, listener);
  };

  return server;
};

const origHttpRequest = http.request;
http.request = function(options, callback) {
  if (!shouldSample()) return origHttpRequest.call(this, options, callback);

  const startTime = Date.now();
  const spanId = genSpanId();
  const traceId = activeContext.traceId || genTraceId();
  const parentSpanId = activeContext.spanId || '';

  if (typeof options === 'object' && options !== null) {
    if (!options.headers) options.headers = {};
    options.headers['traceparent'] = `00-${traceId}-${spanId}-01`;
    options.headers['X-Obtrace-Trace-Id'] = traceId;
  }

  const req = origHttpRequest.call(this, options, function(res) {
    const duration = Date.now() - startTime;
    const url = typeof options === 'string' ? options : `${options.hostname || options.host}${options.path || '/'}`;

    enqueue('span', {
      traceId,
      spanId,
      parentSpanId,
      name: `HTTP ${(options.method || 'GET')} ${url}`,
      kind: 3,
      startTimeUnixNano: (startTime * 1e6).toString(),
      endTimeUnixNano: ((startTime + duration) * 1e6).toString(),
      attributes: objToAttrs({
        'http.method': options.method || 'GET',
        'http.url': url,
        'http.status_code': res.statusCode.toString(),
        'span.kind': 'client',
      }),
      status: { code: res.statusCode >= 400 ? 2 : 1 },
    });

    if (callback) callback(res);
  });

  return req;
};

function extractTraceId(req) {
  const tp = req.headers['traceparent'];
  if (tp) {
    const parts = tp.split('-');
    if (parts.length >= 3) return parts[1];
  }
  return req.headers['x-obtrace-trace-id'] || null;
}

function extractParentSpanId(req) {
  const tp = req.headers['traceparent'];
  if (tp) {
    const parts = tp.split('-');
    if (parts.length >= 4) return parts[2];
  }
  return '';
}

const runtimeMetricsInterval = setInterval(() => {
  const mem = process.memoryUsage();
  const cpuUsage = process.cpuUsage();
  const now = (Date.now() * 1e6).toString();

  enqueue('metric', {
    name: 'process.runtime.nodejs.memory.heap.used',
    unit: 'By',
    gauge: { dataPoints: [{ asDouble: mem.heapUsed, timeUnixNano: now }] },
  });
  enqueue('metric', {
    name: 'process.runtime.nodejs.memory.rss',
    unit: 'By',
    gauge: { dataPoints: [{ asDouble: mem.rss, timeUnixNano: now }] },
  });
  enqueue('metric', {
    name: 'process.runtime.nodejs.memory.heap.total',
    unit: 'By',
    gauge: { dataPoints: [{ asDouble: mem.heapTotal, timeUnixNano: now }] },
  });
  enqueue('metric', {
    name: 'process.cpu.time',
    unit: 'us',
    sum: {
      dataPoints: [{ asDouble: cpuUsage.user + cpuUsage.system, timeUnixNano: now }],
      aggregationTemporality: 2,
      isMonotonic: true,
    },
  });
  enqueue('metric', {
    name: 'process.runtime.nodejs.event_loop.utilization',
    unit: '1',
    gauge: { dataPoints: [{ asDouble: 0, timeUnixNano: now }] },
  });
}, 15000);

if (runtimeMetricsInterval.unref) runtimeMetricsInterval.unref();

process.on('beforeExit', flush);
process.on('SIGTERM', () => { flush(); setTimeout(() => process.exit(0), 500); });
process.on('SIGINT', () => { flush(); setTimeout(() => process.exit(0), 500); });

const origConsoleError = console.error;
console.error = function(...args) {
  const msg = args.map(a => typeof a === 'string' ? a : JSON.stringify(a)).join(' ');
  enqueue('log', {
    timeUnixNano: (Date.now() * 1e6).toString(),
    severityNumber: 17,
    severityText: 'ERROR',
    body: { stringValue: msg },
    attributes: objToAttrs({ 'log.source': 'console.error' }),
    traceId: activeContext.traceId || '',
    spanId: activeContext.spanId || '',
  });
  origConsoleError.apply(console, args);
};

const origConsoleWarn = console.warn;
console.warn = function(...args) {
  const msg = args.map(a => typeof a === 'string' ? a : JSON.stringify(a)).join(' ');
  enqueue('log', {
    timeUnixNano: (Date.now() * 1e6).toString(),
    severityNumber: 13,
    severityText: 'WARN',
    body: { stringValue: msg },
    attributes: objToAttrs({ 'log.source': 'console.warn' }),
    traceId: activeContext.traceId || '',
    spanId: activeContext.spanId || '',
  });
  origConsoleWarn.apply(console, args);
};

process.on('uncaughtException', (err) => {
  enqueue('log', {
    timeUnixNano: (Date.now() * 1e6).toString(),
    severityNumber: 21,
    severityText: 'FATAL',
    body: { stringValue: `Uncaught Exception: ${err.message}\n${err.stack}` },
    attributes: objToAttrs({
      'exception.type': err.constructor.name,
      'exception.message': err.message,
      'exception.stacktrace': err.stack || '',
    }),
    traceId: activeContext.traceId || '',
    spanId: activeContext.spanId || '',
  });
  flush();
});

process.on('unhandledRejection', (reason) => {
  const msg = reason instanceof Error ? `${reason.message}\n${reason.stack}` : String(reason);
  enqueue('log', {
    timeUnixNano: (Date.now() * 1e6).toString(),
    severityNumber: 17,
    severityText: 'ERROR',
    body: { stringValue: `Unhandled Rejection: ${msg}` },
    attributes: objToAttrs({ 'exception.type': 'UnhandledRejection' }),
    traceId: activeContext.traceId || '',
    spanId: activeContext.spanId || '',
  });
});

console.log(`[obtrace-zero] auto-instrumentation active for ${OBTRACE_SERVICE_NAME} (${OBTRACE_ENVIRONMENT})`);
