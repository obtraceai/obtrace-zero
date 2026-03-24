<?php
/**
 * obtrace-zero PHP Auto-Instrumentation Loader
 *
 * Injected via PHP_INI_SCAN_DIR with auto_prepend_file=/obtrace/obtrace_loader.php
 * Instruments: Laravel, Symfony, vanilla PHP HTTP
 */

$OBTRACE_API_KEY = getenv('OBTRACE_API_KEY') ?: '';
$OBTRACE_INGEST_URL = getenv('OBTRACE_INGEST_URL') ?: '';
$OBTRACE_SERVICE_NAME = getenv('OBTRACE_SERVICE_NAME') ?: 'unknown';
$OBTRACE_ENVIRONMENT = getenv('OBTRACE_ENVIRONMENT') ?: 'unknown';
$OBTRACE_POD_NAME = getenv('OBTRACE_POD_NAME') ?: '';
$OBTRACE_POD_NAMESPACE = getenv('OBTRACE_POD_NAMESPACE') ?: '';
$OBTRACE_NODE_NAME = getenv('OBTRACE_NODE_NAME') ?: '';
$OBTRACE_SAMPLE_RATIO = (float)(getenv('OBTRACE_TRACE_SAMPLE_RATIO') ?: '1.0');

if (empty($OBTRACE_API_KEY) || empty($OBTRACE_INGEST_URL)) {
    return;
}

class ObtraceZero {
    private static $queue = [];
    private static $maxQueue = 500;
    private static $apiKey;
    private static $ingestUrl;
    private static $serviceName;
    private static $environment;
    private static $resourceAttrs;
    private static $traceId = null;
    private static $spanId = null;
    private static $sampleRatio;
    private static $startTime;

    public static function init($apiKey, $ingestUrl, $serviceName, $environment, $podName, $podNs, $nodeName, $sampleRatio) {
        self::$apiKey = $apiKey;
        self::$ingestUrl = $ingestUrl;
        self::$serviceName = $serviceName;
        self::$environment = $environment;
        self::$sampleRatio = $sampleRatio;
        self::$startTime = hrtime(true);

        self::$resourceAttrs = [
            'service.name' => $serviceName,
            'deployment.environment' => $environment,
            'k8s.pod.name' => $podName,
            'k8s.namespace.name' => $podNs,
            'k8s.node.name' => $nodeName,
            'telemetry.sdk.name' => 'obtrace-zero',
            'telemetry.sdk.language' => 'php',
            'process.runtime.name' => 'php',
            'process.runtime.version' => PHP_VERSION,
            'process.pid' => (string)getmypid(),
        ];

        if (self::shouldSample()) {
            self::$traceId = self::extractTraceId() ?: self::genTraceId();
            self::$spanId = self::genSpanId();
        }

        register_shutdown_function([self::class, 'shutdown']);
        set_exception_handler([self::class, 'exceptionHandler']);
    }

    public static function shutdown() {
        if (self::$traceId === null) {
            self::flush();
            return;
        }

        $duration = hrtime(true) - self::$startTime;
        $method = $_SERVER['REQUEST_METHOD'] ?? 'CLI';
        $uri = $_SERVER['REQUEST_URI'] ?? '';
        $statusCode = http_response_code() ?: 200;
        $parentSpanId = self::extractParentSpanId();

        self::enqueue('span', [
            'traceId' => self::$traceId,
            'spanId' => self::$spanId,
            'parentSpanId' => $parentSpanId,
            'name' => "$method $uri",
            'kind' => 2,
            'startTimeUnixNano' => (string)self::$startTime,
            'endTimeUnixNano' => (string)hrtime(true),
            'attributes' => self::buildAttrs([
                'http.method' => $method,
                'http.url' => $uri,
                'http.route' => strtok($uri, '?'),
                'http.status_code' => (string)$statusCode,
                'http.user_agent' => $_SERVER['HTTP_USER_AGENT'] ?? '',
                'net.peer.ip' => $_SERVER['REMOTE_ADDR'] ?? '',
            ]),
            'status' => ['code' => $statusCode >= 400 ? 2 : 1],
        ]);

        self::enqueue('metric', [
            'name' => 'http.server.duration',
            'unit' => 'ns',
            'sum' => [
                'dataPoints' => [[
                    'asDouble' => $duration,
                    'timeUnixNano' => (string)hrtime(true),
                    'attributes' => self::buildAttrs([
                        'http.method' => $method,
                        'http.route' => strtok($uri, '?'),
                        'http.status_code' => (string)$statusCode,
                    ]),
                ]],
                'aggregationTemporality' => 1,
                'isMonotonic' => false,
            ],
        ]);

        $mem = memory_get_peak_usage(true);
        self::enqueue('metric', [
            'name' => 'process.runtime.php.memory.peak',
            'unit' => 'By',
            'gauge' => ['dataPoints' => [['asDouble' => $mem, 'timeUnixNano' => (string)hrtime(true)]]],
        ]);

        self::flush();
    }

    public static function exceptionHandler($e) {
        self::enqueue('log', [
            'timeUnixNano' => (string)hrtime(true),
            'severityNumber' => 21,
            'severityText' => 'FATAL',
            'body' => ['stringValue' => "Uncaught " . get_class($e) . ": " . $e->getMessage()],
            'attributes' => self::buildAttrs([
                'exception.type' => get_class($e),
                'exception.message' => $e->getMessage(),
                'exception.stacktrace' => $e->getTraceAsString(),
            ]),
            'traceId' => self::$traceId ?? '',
            'spanId' => self::$spanId ?? '',
        ]);
        self::flush();
    }

    private static function enqueue($type, $payload) {
        if (count(self::$queue) >= self::$maxQueue) {
            array_shift(self::$queue);
        }
        self::$queue[] = ['type' => $type, 'payload' => $payload];
    }

    private static function flush() {
        if (empty(self::$queue)) return;

        $spans = array_filter(self::$queue, fn($b) => $b['type'] === 'span');
        $logs = array_filter(self::$queue, fn($b) => $b['type'] === 'log');
        $metrics = array_filter(self::$queue, fn($b) => $b['type'] === 'metric');
        self::$queue = [];

        $resource = ['attributes' => self::buildAttrs(self::$resourceAttrs)];

        if (!empty($spans)) {
            self::send('/otlp/v1/traces', [
                'resourceSpans' => [[
                    'resource' => $resource,
                    'scopeSpans' => [[
                        'scope' => ['name' => 'obtrace-zero-php'],
                        'spans' => array_values(array_map(fn($s) => $s['payload'], $spans)),
                    ]],
                ]],
            ]);
        }
        if (!empty($logs)) {
            self::send('/otlp/v1/logs', [
                'resourceLogs' => [[
                    'resource' => $resource,
                    'scopeLogs' => [[
                        'scope' => ['name' => 'obtrace-zero-php'],
                        'logRecords' => array_values(array_map(fn($l) => $l['payload'], $logs)),
                    ]],
                ]],
            ]);
        }
        if (!empty($metrics)) {
            self::send('/otlp/v1/metrics', [
                'resourceMetrics' => [[
                    'resource' => $resource,
                    'scopeMetrics' => [[
                        'scope' => ['name' => 'obtrace-zero-php'],
                        'metrics' => array_values(array_map(fn($m) => $m['payload'], $metrics)),
                    ]],
                ]],
            ]);
        }
    }

    private static function send($path, $body) {
        try {
            $url = rtrim(self::$ingestUrl, '/') . $path;
            $json = json_encode($body);
            $ch = curl_init($url);
            curl_setopt_array($ch, [
                CURLOPT_POST => true,
                CURLOPT_POSTFIELDS => $json,
                CURLOPT_HTTPHEADER => [
                    'Content-Type: application/json',
                    'X-API-Key: ' . self::$apiKey,
                    'X-Obtrace-Source: zero-agent-php',
                ],
                CURLOPT_TIMEOUT => 5,
                CURLOPT_RETURNTRANSFER => true,
            ]);
            curl_exec($ch);
            curl_close($ch);
        } catch (\Throwable $e) {}
    }

    private static function buildAttrs($map) {
        $attrs = [];
        foreach ($map as $k => $v) {
            if (!empty($v)) {
                $attrs[] = ['key' => $k, 'value' => ['stringValue' => (string)$v]];
            }
        }
        return $attrs;
    }

    private static function shouldSample() {
        return (mt_rand() / mt_getrandmax()) < self::$sampleRatio;
    }

    private static function genTraceId() {
        return bin2hex(random_bytes(16));
    }

    private static function genSpanId() {
        return bin2hex(random_bytes(8));
    }

    private static function extractTraceId() {
        $tp = $_SERVER['HTTP_TRACEPARENT'] ?? '';
        if ($tp) {
            $parts = explode('-', $tp);
            if (count($parts) >= 3) return $parts[1];
        }
        return $_SERVER['HTTP_X_OBTRACE_TRACE_ID'] ?? null;
    }

    private static function extractParentSpanId() {
        $tp = $_SERVER['HTTP_TRACEPARENT'] ?? '';
        if ($tp) {
            $parts = explode('-', $tp);
            if (count($parts) >= 4) return $parts[2];
        }
        return '';
    }
}

ObtraceZero::init(
    $OBTRACE_API_KEY,
    $OBTRACE_INGEST_URL,
    $OBTRACE_SERVICE_NAME,
    $OBTRACE_ENVIRONMENT,
    $OBTRACE_POD_NAME,
    $OBTRACE_POD_NAMESPACE,
    $OBTRACE_NODE_NAME,
    $OBTRACE_SAMPLE_RATIO
);
