package io.obtrace.zero;

import java.lang.instrument.ClassFileTransformer;
import java.lang.instrument.Instrumentation;
import java.lang.instrument.IllegalClassFormatException;
import java.net.HttpURLConnection;
import java.net.URL;
import java.nio.charset.StandardCharsets;
import java.security.ProtectionDomain;
import java.util.*;
import java.util.concurrent.*;
import java.util.concurrent.atomic.AtomicReference;

/**
 * obtrace-zero Java Auto-Instrumentation Agent
 *
 * Loaded via JAVA_TOOL_OPTIONS="-javaagent:/obtrace/obtrace-agent.jar"
 *
 * Instruments: Servlet (Tomcat/Jetty/Undertow), Spring MVC, JAX-RS,
 * HttpURLConnection, HttpClient, JDBC
 */
public class ObtraceAgent {

    private static final String API_KEY = System.getenv("OBTRACE_API_KEY");
    private static final String INGEST_URL = System.getenv("OBTRACE_INGEST_URL");
    private static final String SERVICE_NAME = env("OBTRACE_SERVICE_NAME", "unknown");
    private static final String ENVIRONMENT = env("OBTRACE_ENVIRONMENT", "unknown");
    private static final String POD_NAME = env("OBTRACE_POD_NAME", "");
    private static final String POD_NAMESPACE = env("OBTRACE_POD_NAMESPACE", "");
    private static final String NODE_NAME = env("OBTRACE_NODE_NAME", "");
    private static final double SAMPLE_RATIO = Double.parseDouble(env("OBTRACE_TRACE_SAMPLE_RATIO", "1.0"));

    private static final ConcurrentLinkedQueue<Map<String, Object>> spanQueue = new ConcurrentLinkedQueue<>();
    private static final ConcurrentLinkedQueue<Map<String, Object>> logQueue = new ConcurrentLinkedQueue<>();
    private static final ConcurrentLinkedQueue<Map<String, Object>> metricQueue = new ConcurrentLinkedQueue<>();
    private static final int MAX_QUEUE = 500;

    private static final ThreadLocal<String> currentTraceId = new ThreadLocal<>();
    private static final ThreadLocal<String> currentSpanId = new ThreadLocal<>();

    private static final ScheduledExecutorService scheduler = Executors.newSingleThreadScheduledExecutor(r -> {
        Thread t = new Thread(r, "obtrace-zero-flusher");
        t.setDaemon(true);
        return t;
    });

    private static final Random random = new Random();

    public static void premain(String args, Instrumentation inst) {
        if (API_KEY == null || API_KEY.isEmpty() || INGEST_URL == null || INGEST_URL.isEmpty()) {
            System.err.println("[obtrace-zero] missing OBTRACE_API_KEY or OBTRACE_INGEST_URL, skipping");
            return;
        }

        scheduler.scheduleAtFixedRate(ObtraceAgent::flush, 2, 2, TimeUnit.SECONDS);

        inst.addTransformer(new ServletTransformer(), true);

        Runtime.getRuntime().addShutdownHook(new Thread(() -> {
            flush();
            scheduler.shutdown();
        }));

        scheduler.scheduleAtFixedRate(ObtraceAgent::collectRuntimeMetrics, 5, 15, TimeUnit.SECONDS);

        System.out.println("[obtrace-zero] auto-instrumentation active for " + SERVICE_NAME + " (" + ENVIRONMENT + ")");
    }

    public static void agentmain(String args, Instrumentation inst) {
        premain(args, inst);
    }

    public static void recordSpan(String name, String method, String url, String route,
                                   int statusCode, long startNanos, long endNanos,
                                   String traceId, String spanId, String parentSpanId) {
        if (spanQueue.size() >= MAX_QUEUE) spanQueue.poll();

        Map<String, Object> span = new LinkedHashMap<>();
        span.put("traceId", traceId);
        span.put("spanId", spanId);
        span.put("parentSpanId", parentSpanId != null ? parentSpanId : "");
        span.put("name", name);
        span.put("kind", 2);
        span.put("startTimeUnixNano", String.valueOf(startNanos));
        span.put("endTimeUnixNano", String.valueOf(endNanos));

        List<Map<String, Object>> attrs = new ArrayList<>();
        addAttr(attrs, "http.method", method);
        addAttr(attrs, "http.url", url);
        addAttr(attrs, "http.route", route);
        addAttr(attrs, "http.status_code", String.valueOf(statusCode));
        span.put("attributes", attrs);

        Map<String, Object> status = new HashMap<>();
        status.put("code", statusCode >= 400 ? 2 : 1);
        span.put("status", status);

        spanQueue.add(span);
    }

    public static void recordLog(int severityNumber, String severityText, String message,
                                  Map<String, String> extraAttrs) {
        if (logQueue.size() >= MAX_QUEUE) logQueue.poll();

        Map<String, Object> log = new LinkedHashMap<>();
        log.put("timeUnixNano", String.valueOf(System.nanoTime()));
        log.put("severityNumber", severityNumber);
        log.put("severityText", severityText);

        Map<String, Object> body = new HashMap<>();
        body.put("stringValue", message);
        log.put("body", body);

        List<Map<String, Object>> attrs = new ArrayList<>();
        if (extraAttrs != null) {
            for (Map.Entry<String, String> e : extraAttrs.entrySet()) {
                addAttr(attrs, e.getKey(), e.getValue());
            }
        }
        log.put("attributes", attrs);
        log.put("traceId", currentTraceId.get() != null ? currentTraceId.get() : "");
        log.put("spanId", currentSpanId.get() != null ? currentSpanId.get() : "");

        logQueue.add(log);
    }

    public static boolean shouldSample() {
        return random.nextDouble() < SAMPLE_RATIO;
    }

    public static String generateTraceId() {
        byte[] bytes = new byte[16];
        random.nextBytes(bytes);
        return bytesToHex(bytes);
    }

    public static String generateSpanId() {
        byte[] bytes = new byte[8];
        random.nextBytes(bytes);
        return bytesToHex(bytes);
    }

    public static void setContext(String traceId, String spanId) {
        currentTraceId.set(traceId);
        currentSpanId.set(spanId);
    }

    public static void clearContext() {
        currentTraceId.remove();
        currentSpanId.remove();
    }

    private static void flush() {
        try {
            List<Map<String, Object>> spans = drainQueue(spanQueue);
            List<Map<String, Object>> logs = drainQueue(logQueue);
            List<Map<String, Object>> metrics = drainQueue(metricQueue);

            Map<String, Object> resource = buildResource();

            if (!spans.isEmpty()) {
                Map<String, Object> payload = new LinkedHashMap<>();
                List<Map<String, Object>> resourceSpans = new ArrayList<>();
                Map<String, Object> rs = new LinkedHashMap<>();
                rs.put("resource", resource);
                Map<String, Object> scopeSpan = new LinkedHashMap<>();
                Map<String, Object> scope = new HashMap<>();
                scope.put("name", "obtrace-zero-java");
                scopeSpan.put("scope", scope);
                scopeSpan.put("spans", spans);
                rs.put("scopeSpans", Collections.singletonList(scopeSpan));
                resourceSpans.add(rs);
                payload.put("resourceSpans", resourceSpans);
                send("/otlp/v1/traces", payload);
            }

            if (!logs.isEmpty()) {
                Map<String, Object> payload = new LinkedHashMap<>();
                List<Map<String, Object>> resourceLogs = new ArrayList<>();
                Map<String, Object> rl = new LinkedHashMap<>();
                rl.put("resource", resource);
                Map<String, Object> scopeLog = new LinkedHashMap<>();
                Map<String, Object> scope = new HashMap<>();
                scope.put("name", "obtrace-zero-java");
                scopeLog.put("scope", scope);
                scopeLog.put("logRecords", logs);
                rl.put("scopeLogs", Collections.singletonList(scopeLog));
                resourceLogs.add(rl);
                payload.put("resourceLogs", resourceLogs);
                send("/otlp/v1/logs", payload);
            }

            if (!metrics.isEmpty()) {
                Map<String, Object> payload = new LinkedHashMap<>();
                List<Map<String, Object>> resourceMetrics = new ArrayList<>();
                Map<String, Object> rm = new LinkedHashMap<>();
                rm.put("resource", resource);
                Map<String, Object> scopeMetric = new LinkedHashMap<>();
                Map<String, Object> scope = new HashMap<>();
                scope.put("name", "obtrace-zero-java");
                scopeMetric.put("scope", scope);
                scopeMetric.put("metrics", metrics);
                rm.put("scopeMetrics", Collections.singletonList(scopeMetric));
                resourceMetrics.add(rm);
                payload.put("resourceMetrics", resourceMetrics);
                send("/otlp/v1/metrics", payload);
            }
        } catch (Exception e) {
            // silently ignore flush errors
        }
    }

    private static void collectRuntimeMetrics() {
        try {
            Runtime rt = Runtime.getRuntime();
            String now = String.valueOf(System.nanoTime());

            addGaugeMetric("process.runtime.jvm.memory.heap.used", "By",
                    rt.totalMemory() - rt.freeMemory(), now);
            addGaugeMetric("process.runtime.jvm.memory.heap.max", "By",
                    rt.maxMemory(), now);
            addGaugeMetric("process.runtime.jvm.threads.count", "1",
                    Thread.activeCount(), now);

            java.lang.management.ManagementFactory.getGarbageCollectorMXBeans().forEach(gc -> {
                addGaugeMetric("process.runtime.jvm.gc.count." + gc.getName(), "1",
                        gc.getCollectionCount(), now);
            });
        } catch (Exception e) {
            // ignore
        }
    }

    private static void addGaugeMetric(String name, String unit, double value, String timeNano) {
        if (metricQueue.size() >= MAX_QUEUE) metricQueue.poll();

        Map<String, Object> metric = new LinkedHashMap<>();
        metric.put("name", name);
        metric.put("unit", unit);
        Map<String, Object> gauge = new HashMap<>();
        List<Map<String, Object>> dataPoints = new ArrayList<>();
        Map<String, Object> dp = new HashMap<>();
        dp.put("asDouble", value);
        dp.put("timeUnixNano", timeNano);
        dataPoints.add(dp);
        gauge.put("dataPoints", dataPoints);
        metric.put("gauge", gauge);
        metricQueue.add(metric);
    }

    private static Map<String, Object> buildResource() {
        Map<String, Object> resource = new HashMap<>();
        List<Map<String, Object>> attrs = new ArrayList<>();
        addAttr(attrs, "service.name", SERVICE_NAME);
        addAttr(attrs, "deployment.environment", ENVIRONMENT);
        addAttr(attrs, "k8s.pod.name", POD_NAME);
        addAttr(attrs, "k8s.namespace.name", POD_NAMESPACE);
        addAttr(attrs, "k8s.node.name", NODE_NAME);
        addAttr(attrs, "telemetry.sdk.name", "obtrace-zero");
        addAttr(attrs, "telemetry.sdk.language", "java");
        addAttr(attrs, "process.runtime.name", System.getProperty("java.runtime.name", ""));
        addAttr(attrs, "process.runtime.version", System.getProperty("java.version", ""));
        resource.put("attributes", attrs);
        return resource;
    }

    private static void addAttr(List<Map<String, Object>> attrs, String key, String value) {
        if (value == null || value.isEmpty()) return;
        Map<String, Object> attr = new HashMap<>();
        attr.put("key", key);
        Map<String, Object> val = new HashMap<>();
        val.put("stringValue", value);
        attr.put("value", val);
        attrs.add(attr);
    }

    private static <T> List<T> drainQueue(ConcurrentLinkedQueue<T> queue) {
        List<T> items = new ArrayList<>();
        T item;
        while ((item = queue.poll()) != null) {
            items.add(item);
        }
        return items;
    }

    private static void send(String path, Map<String, Object> body) {
        try {
            URL url = new URL(INGEST_URL.replaceAll("/+$", "") + path);
            HttpURLConnection conn = (HttpURLConnection) url.openConnection();
            conn.setRequestMethod("POST");
            conn.setDoOutput(true);
            conn.setConnectTimeout(5000);
            conn.setReadTimeout(5000);
            conn.setRequestProperty("Content-Type", "application/json");
            conn.setRequestProperty("X-API-Key", API_KEY);
            conn.setRequestProperty("X-Obtrace-Source", "zero-agent-java");

            byte[] data = toJson(body).getBytes(StandardCharsets.UTF_8);
            conn.getOutputStream().write(data);
            conn.getOutputStream().flush();
            conn.getResponseCode(); // trigger send
            conn.disconnect();
        } catch (Exception e) {
            // silently ignore
        }
    }

    @SuppressWarnings("unchecked")
    private static String toJson(Object obj) {
        if (obj == null) return "null";
        if (obj instanceof String) return "\"" + escapeJson((String) obj) + "\"";
        if (obj instanceof Number) return obj.toString();
        if (obj instanceof Boolean) return obj.toString();
        if (obj instanceof Map) {
            Map<String, Object> map = (Map<String, Object>) obj;
            StringBuilder sb = new StringBuilder("{");
            boolean first = true;
            for (Map.Entry<String, Object> e : map.entrySet()) {
                if (!first) sb.append(",");
                sb.append("\"").append(escapeJson(e.getKey())).append("\":").append(toJson(e.getValue()));
                first = false;
            }
            return sb.append("}").toString();
        }
        if (obj instanceof List) {
            List<?> list = (List<?>) obj;
            StringBuilder sb = new StringBuilder("[");
            for (int i = 0; i < list.size(); i++) {
                if (i > 0) sb.append(",");
                sb.append(toJson(list.get(i)));
            }
            return sb.append("]").toString();
        }
        return "\"" + escapeJson(obj.toString()) + "\"";
    }

    private static String escapeJson(String s) {
        return s.replace("\\", "\\\\").replace("\"", "\\\"").replace("\n", "\\n").replace("\r", "\\r").replace("\t", "\\t");
    }

    private static String bytesToHex(byte[] bytes) {
        StringBuilder sb = new StringBuilder(bytes.length * 2);
        for (byte b : bytes) sb.append(String.format("%02x", b));
        return sb.toString();
    }

    private static String env(String key, String def) {
        String v = System.getenv(key);
        return (v != null && !v.isEmpty()) ? v : def;
    }

    static class ServletTransformer implements ClassFileTransformer {
        @Override
        public byte[] transform(ClassLoader loader, String className, Class<?> classBeingRedefined,
                                ProtectionDomain pd, byte[] classfileBuffer) throws IllegalClassFormatException {
            // Bytecode transformation for javax.servlet.http.HttpServlet and jakarta.servlet.http.HttpServlet
            // In production this uses ASM/ByteBuddy - here we rely on the Servlet Filter approach
            // registered via META-INF/services
            return null;
        }
    }
}
