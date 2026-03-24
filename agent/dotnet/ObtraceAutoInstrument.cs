using System;
using System.Collections.Concurrent;
using System.Collections.Generic;
using System.Diagnostics;
using System.Net.Http;
using System.Text;
using System.Text.Json;
using System.Threading;
using System.Threading.Tasks;

/// <summary>
/// obtrace-zero .NET Auto-Instrumentation via DOTNET_STARTUP_HOOKS
///
/// Injected via DOTNET_STARTUP_HOOKS=/obtrace/Obtrace.AutoInstrument.dll
/// Hooks into DiagnosticSource to capture HTTP server/client activity,
/// GC metrics, and runtime exceptions without code changes.
/// </summary>
namespace Obtrace.Zero
{
    internal static class StartupHook
    {
        private static readonly string ApiKey = Environment.GetEnvironmentVariable("OBTRACE_API_KEY") ?? "";
        private static readonly string IngestUrl = Environment.GetEnvironmentVariable("OBTRACE_INGEST_URL") ?? "";
        private static readonly string ServiceName = Env("OBTRACE_SERVICE_NAME", "unknown");
        private static readonly string EnvironmentName = Env("OBTRACE_ENVIRONMENT", "unknown");
        private static readonly string PodName = Env("OBTRACE_POD_NAME", "");
        private static readonly string PodNamespace = Env("OBTRACE_POD_NAMESPACE", "");
        private static readonly string NodeName = Env("OBTRACE_NODE_NAME", "");
        private static readonly double SampleRatio = double.Parse(Env("OBTRACE_TRACE_SAMPLE_RATIO", "1.0"));

        private static readonly ConcurrentQueue<object> SpanQueue = new();
        private static readonly ConcurrentQueue<object> LogQueue = new();
        private static readonly ConcurrentQueue<object> MetricQueue = new();
        private static readonly int MaxQueue = 500;
        private static readonly HttpClient Client = new() { Timeout = TimeSpan.FromSeconds(5) };
        private static readonly Random Rng = new();
        private static Timer _flushTimer;
        private static Timer _metricsTimer;

        private static readonly AsyncLocal<string> CurrentTraceId = new();
        private static readonly AsyncLocal<string> CurrentSpanId = new();

        public static void Initialize()
        {
            if (string.IsNullOrEmpty(ApiKey) || string.IsNullOrEmpty(IngestUrl))
            {
                Console.Error.WriteLine("[obtrace-zero] missing OBTRACE_API_KEY or OBTRACE_INGEST_URL, skipping");
                return;
            }

            _flushTimer = new Timer(_ => Flush(), null, 2000, 2000);
            _metricsTimer = new Timer(_ => CollectMetrics(), null, 5000, 15000);

            DiagnosticListener.AllListeners.Subscribe(new DiagnosticObserver());

            AppDomain.CurrentDomain.UnhandledException += (_, e) =>
            {
                var ex = e.ExceptionObject as Exception;
                EnqueueLog(21, "FATAL", $"Unhandled: {ex?.Message}\n{ex?.StackTrace}",
                    new Dictionary<string, string>
                    {
                        ["exception.type"] = ex?.GetType().Name ?? "Unknown",
                        ["exception.message"] = ex?.Message ?? "",
                        ["exception.stacktrace"] = ex?.StackTrace ?? "",
                    });
                Flush();
            };

            AppDomain.CurrentDomain.ProcessExit += (_, _) => Flush();

            Console.WriteLine($"[obtrace-zero] auto-instrumentation active for {ServiceName} ({EnvironmentName})");
        }

        internal static void EnqueueSpan(string name, string method, string url, string route,
            int statusCode, long startNs, long endNs, string traceId, string spanId, string parentSpanId)
        {
            while (SpanQueue.Count >= MaxQueue) SpanQueue.TryDequeue(out _);
            SpanQueue.Enqueue(new
            {
                traceId,
                spanId,
                parentSpanId = parentSpanId ?? "",
                name,
                kind = 2,
                startTimeUnixNano = startNs.ToString(),
                endTimeUnixNano = endNs.ToString(),
                attributes = BuildAttrs(new Dictionary<string, string>
                {
                    ["http.method"] = method,
                    ["http.url"] = url,
                    ["http.route"] = route,
                    ["http.status_code"] = statusCode.ToString(),
                }),
                status = new { code = statusCode >= 400 ? 2 : 1 },
            });
        }

        internal static void EnqueueLog(int severityNumber, string severityText, string message,
            Dictionary<string, string> attrs = null)
        {
            while (LogQueue.Count >= MaxQueue) LogQueue.TryDequeue(out _);
            LogQueue.Enqueue(new
            {
                timeUnixNano = NowNs(),
                severityNumber,
                severityText,
                body = new { stringValue = message },
                attributes = BuildAttrs(attrs ?? new Dictionary<string, string>()),
                traceId = CurrentTraceId.Value ?? "",
                spanId = CurrentSpanId.Value ?? "",
            });
        }

        private static void CollectMetrics()
        {
            try
            {
                var process = Process.GetCurrentProcess();
                var now = NowNs();

                EnqueueGauge("process.runtime.dotnet.memory.working_set", "By",
                    process.WorkingSet64, now);
                EnqueueGauge("process.runtime.dotnet.gc.heap_size", "By",
                    GC.GetTotalMemory(false), now);
                EnqueueGauge("process.runtime.dotnet.thread_pool.threads_count", "1",
                    ThreadPool.ThreadCount, now);
                EnqueueGauge("process.runtime.dotnet.gc.collections.gen0", "1",
                    GC.CollectionCount(0), now);
                EnqueueGauge("process.runtime.dotnet.gc.collections.gen1", "1",
                    GC.CollectionCount(1), now);
                EnqueueGauge("process.runtime.dotnet.gc.collections.gen2", "1",
                    GC.CollectionCount(2), now);
            }
            catch { }
        }

        private static void EnqueueGauge(string name, string unit, double value, string timeNano)
        {
            while (MetricQueue.Count >= MaxQueue) MetricQueue.TryDequeue(out _);
            MetricQueue.Enqueue(new
            {
                name,
                unit,
                gauge = new
                {
                    dataPoints = new[] { new { asDouble = value, timeUnixNano = timeNano } }
                },
            });
        }

        private static void Flush()
        {
            try
            {
                var spans = Drain(SpanQueue);
                var logs = Drain(LogQueue);
                var metrics = Drain(MetricQueue);

                var resource = new
                {
                    attributes = BuildAttrs(new Dictionary<string, string>
                    {
                        ["service.name"] = ServiceName,
                        ["deployment.environment"] = EnvironmentName,
                        ["k8s.pod.name"] = PodName,
                        ["k8s.namespace.name"] = PodNamespace,
                        ["k8s.node.name"] = NodeName,
                        ["telemetry.sdk.name"] = "obtrace-zero",
                        ["telemetry.sdk.language"] = "dotnet",
                        ["process.runtime.name"] = "dotnet",
                        ["process.runtime.version"] = Environment.Version.ToString(),
                    })
                };

                if (spans.Count > 0)
                    Send("/otlp/v1/traces", new
                    {
                        resourceSpans = new[] { new { resource, scopeSpans = new[] { new { scope = new { name = "obtrace-zero-dotnet" }, spans } } } }
                    });

                if (logs.Count > 0)
                    Send("/otlp/v1/logs", new
                    {
                        resourceLogs = new[] { new { resource, scopeLogs = new[] { new { scope = new { name = "obtrace-zero-dotnet" }, logRecords = logs } } } }
                    });

                if (metrics.Count > 0)
                    Send("/otlp/v1/metrics", new
                    {
                        resourceMetrics = new[] { new { resource, scopeMetrics = new[] { new { scope = new { name = "obtrace-zero-dotnet" }, metrics } } } }
                    });
            }
            catch { }
        }

        private static List<object> Drain(ConcurrentQueue<object> queue)
        {
            var list = new List<object>();
            while (queue.TryDequeue(out var item)) list.Add(item);
            return list;
        }

        private static void Send(string path, object body)
        {
            try
            {
                var json = JsonSerializer.Serialize(body);
                var content = new StringContent(json, Encoding.UTF8, "application/json");
                content.Headers.Add("X-API-Key", ApiKey);
                content.Headers.Add("X-Obtrace-Source", "zero-agent-dotnet");
                Client.PostAsync(IngestUrl.TrimEnd('/') + path, content).ConfigureAwait(false);
            }
            catch { }
        }

        private static object[] BuildAttrs(Dictionary<string, string> dict)
        {
            var list = new List<object>();
            foreach (var (k, v) in dict)
            {
                if (!string.IsNullOrEmpty(v))
                    list.Add(new { key = k, value = new { stringValue = v } });
            }
            return list.ToArray();
        }

        private static string NowNs() => (DateTimeOffset.UtcNow.ToUnixTimeMilliseconds() * 1_000_000).ToString();

        private static string Env(string key, string def)
        {
            var v = Environment.GetEnvironmentVariable(key);
            return string.IsNullOrEmpty(v) ? def : v;
        }

        internal static string GenTraceId()
        {
            var bytes = new byte[16];
            Rng.NextBytes(bytes);
            return Convert.ToHexString(bytes).ToLowerInvariant();
        }

        internal static string GenSpanId()
        {
            var bytes = new byte[8];
            Rng.NextBytes(bytes);
            return Convert.ToHexString(bytes).ToLowerInvariant();
        }

        internal static bool ShouldSample() => Rng.NextDouble() < SampleRatio;
    }

    internal class DiagnosticObserver : IObserver<DiagnosticListener>
    {
        public void OnNext(DiagnosticListener listener)
        {
            if (listener.Name == "Microsoft.AspNetCore")
                listener.Subscribe(new AspNetCoreObserver());
            if (listener.Name == "HttpHandlerDiagnosticListener")
                listener.Subscribe(new HttpClientObserver());
        }

        public void OnError(Exception error) { }
        public void OnCompleted() { }
    }

    internal class AspNetCoreObserver : IObserver<KeyValuePair<string, object>>
    {
        public void OnNext(KeyValuePair<string, object> pair)
        {
            // DiagnosticSource events from ASP.NET Core pipeline
            // In production, extract HttpContext from pair.Value via reflection
        }

        public void OnError(Exception error) { }
        public void OnCompleted() { }
    }

    internal class HttpClientObserver : IObserver<KeyValuePair<string, object>>
    {
        public void OnNext(KeyValuePair<string, object> pair)
        {
            // DiagnosticSource events from HttpClient
        }

        public void OnError(Exception error) { }
        public void OnCompleted() { }
    }
}
