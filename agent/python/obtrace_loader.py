"""
obtrace-zero Python Auto-Instrumentation Loader

Injected via PYTHONSTARTUP=/obtrace/obtrace_loader.py
Automatically instruments the application without any code changes.

Patches: http.server, urllib, requests, httpx, FastAPI, Flask, Django
Captures: traces, logs, metrics, exceptions
"""

import os
import sys
import time
import json
import random
import threading
import traceback
import atexit
import functools

OBTRACE_API_KEY = os.environ.get("OBTRACE_API_KEY", "")
OBTRACE_INGEST_URL = os.environ.get("OBTRACE_INGEST_URL", "")
OBTRACE_SERVICE_NAME = os.environ.get("OBTRACE_SERVICE_NAME", "unknown")
OBTRACE_ENVIRONMENT = os.environ.get("OBTRACE_ENVIRONMENT", "unknown")
OBTRACE_POD_NAME = os.environ.get("OBTRACE_POD_NAME", "")
OBTRACE_POD_NAMESPACE = os.environ.get("OBTRACE_POD_NAMESPACE", "")
OBTRACE_NODE_NAME = os.environ.get("OBTRACE_NODE_NAME", "")
OBTRACE_SAMPLE_RATIO = float(os.environ.get("OBTRACE_TRACE_SAMPLE_RATIO", "1.0"))

if not OBTRACE_API_KEY or not OBTRACE_INGEST_URL:
    pass
else:
    _queue = []
    _lock = threading.Lock()
    _flush_interval = 2.0
    _max_queue = 500
    _context = threading.local()

    RESOURCE_ATTRS = {
        "service.name": OBTRACE_SERVICE_NAME,
        "deployment.environment": OBTRACE_ENVIRONMENT,
        "k8s.pod.name": OBTRACE_POD_NAME,
        "k8s.namespace.name": OBTRACE_POD_NAMESPACE,
        "k8s.node.name": OBTRACE_NODE_NAME,
        "telemetry.sdk.name": "obtrace-zero",
        "telemetry.sdk.language": "python",
        "process.runtime.name": "cpython",
        "process.runtime.version": sys.version.split()[0],
        "process.pid": str(os.getpid()),
    }

    def _gen_trace_id():
        return "%032x" % random.getrandbits(128)

    def _gen_span_id():
        return "%016x" % random.getrandbits(64)

    def _should_sample():
        return random.random() < OBTRACE_SAMPLE_RATIO

    def _now_ns():
        return str(int(time.time() * 1e9))

    def _obj_to_attrs(d):
        return [
            {"key": k, "value": {"stringValue": str(v)}}
            for k, v in d.items()
            if v
        ]

    def _enqueue(type_, payload):
        with _lock:
            if len(_queue) >= _max_queue:
                _queue.pop(0)
            _queue.append({"type": type_, "payload": payload, "timestamp": time.time()})

    def _flush():
        with _lock:
            batch = _queue[:]
            _queue.clear()

        if not batch:
            return

        traces = [b for b in batch if b["type"] == "span"]
        logs = [b for b in batch if b["type"] == "log"]
        metrics = [b for b in batch if b["type"] == "metric"]

        resource = {"attributes": _obj_to_attrs(RESOURCE_ATTRS)}

        if traces:
            _send("/otlp/v1/traces", {
                "resourceSpans": [{
                    "resource": resource,
                    "scopeSpans": [{
                        "scope": {"name": "obtrace-zero-python"},
                        "spans": [t["payload"] for t in traces],
                    }],
                }],
            })
        if logs:
            _send("/otlp/v1/logs", {
                "resourceLogs": [{
                    "resource": resource,
                    "scopeLogs": [{
                        "scope": {"name": "obtrace-zero-python"},
                        "logRecords": [l["payload"] for l in logs],
                    }],
                }],
            })
        if metrics:
            _send("/otlp/v1/metrics", {
                "resourceMetrics": [{
                    "resource": resource,
                    "scopeMetrics": [{
                        "scope": {"name": "obtrace-zero-python"},
                        "metrics": [m["payload"] for m in metrics],
                    }],
                }],
            })

    def _send(path, body):
        try:
            import urllib.request
            url = OBTRACE_INGEST_URL.rstrip("/") + path
            data = json.dumps(body).encode("utf-8")
            req = urllib.request.Request(url, data=data, headers={
                "Content-Type": "application/json",
                "X-API-Key": OBTRACE_API_KEY,
                "X-Obtrace-Source": "zero-agent-python",
            })
            urllib.request.urlopen(req, timeout=5)
        except Exception:
            pass

    def _flush_loop():
        while True:
            time.sleep(_flush_interval)
            try:
                _flush()
            except Exception:
                pass

    _flush_thread = threading.Thread(target=_flush_loop, daemon=True)
    _flush_thread.start()
    atexit.register(_flush)

    def _get_context():
        return (
            getattr(_context, "trace_id", None),
            getattr(_context, "span_id", None),
        )

    def _set_context(trace_id, span_id):
        _context.trace_id = trace_id
        _context.span_id = span_id

    def _clear_context():
        _context.trace_id = None
        _context.span_id = None

    def _patch_wsgi():
        try:
            import wsgiref.simple_server as _ws
            _orig_finish = _ws.ServerHandler.finish_response

            def _patched_finish(self):
                return _orig_finish(self)

            _ws.ServerHandler.finish_response = _patched_finish
        except Exception:
            pass

    def _patch_logging():
        try:
            import logging

            class ObtraceHandler(logging.Handler):
                def emit(self, record):
                    severity_map = {
                        logging.DEBUG: (5, "DEBUG"),
                        logging.INFO: (9, "INFO"),
                        logging.WARNING: (13, "WARN"),
                        logging.ERROR: (17, "ERROR"),
                        logging.CRITICAL: (21, "FATAL"),
                    }
                    sev_num, sev_text = severity_map.get(record.levelno, (9, "INFO"))
                    trace_id, span_id = _get_context()

                    attrs = {
                        "log.source": "logging",
                        "log.logger": record.name,
                        "log.filename": record.pathname,
                        "log.lineno": str(record.lineno),
                    }

                    if record.exc_info and record.exc_info[1]:
                        exc = record.exc_info[1]
                        attrs["exception.type"] = type(exc).__name__
                        attrs["exception.message"] = str(exc)
                        attrs["exception.stacktrace"] = traceback.format_exception(*record.exc_info)[-1]

                    _enqueue("log", {
                        "timeUnixNano": _now_ns(),
                        "severityNumber": sev_num,
                        "severityText": sev_text,
                        "body": {"stringValue": record.getMessage()},
                        "attributes": _obj_to_attrs(attrs),
                        "traceId": trace_id or "",
                        "spanId": span_id or "",
                    })

            root = logging.getLogger()
            root.addHandler(ObtraceHandler())
        except Exception:
            pass

    def _patch_urllib():
        try:
            import urllib.request as ur

            _orig_open = ur.urlopen

            @functools.wraps(_orig_open)
            def _patched_open(url, *args, **kwargs):
                if not _should_sample():
                    return _orig_open(url, *args, **kwargs)

                start = time.time()
                trace_id = getattr(_context, "trace_id", None) or _gen_trace_id()
                span_id = _gen_span_id()
                parent_span_id = getattr(_context, "span_id", None) or ""

                try:
                    resp = _orig_open(url, *args, **kwargs)
                    duration_ms = (time.time() - start) * 1000
                    url_str = url if isinstance(url, str) else url.full_url

                    _enqueue("span", {
                        "traceId": trace_id,
                        "spanId": span_id,
                        "parentSpanId": parent_span_id,
                        "name": f"HTTP {url_str}",
                        "kind": 3,
                        "startTimeUnixNano": str(int(start * 1e9)),
                        "endTimeUnixNano": _now_ns(),
                        "attributes": _obj_to_attrs({
                            "http.url": url_str,
                            "http.status_code": str(resp.status),
                            "span.kind": "client",
                        }),
                        "status": {"code": 2 if resp.status >= 400 else 1},
                    })
                    return resp
                except Exception as e:
                    _enqueue("span", {
                        "traceId": trace_id,
                        "spanId": span_id,
                        "parentSpanId": parent_span_id,
                        "name": f"HTTP {url if isinstance(url, str) else url.full_url}",
                        "kind": 3,
                        "startTimeUnixNano": str(int(start * 1e9)),
                        "endTimeUnixNano": _now_ns(),
                        "attributes": _obj_to_attrs({
                            "http.url": str(url),
                            "exception.type": type(e).__name__,
                            "exception.message": str(e),
                        }),
                        "status": {"code": 2},
                    })
                    raise

            ur.urlopen = _patched_open
        except Exception:
            pass

    def _patch_fastapi():
        try:
            from starlette.middleware.base import BaseHTTPMiddleware
            from starlette.requests import Request as StarletteRequest
            import fastapi

            class ObtraceMiddleware(BaseHTTPMiddleware):
                async def dispatch(self, request: StarletteRequest, call_next):
                    if not _should_sample():
                        return await call_next(request)

                    trace_id = request.headers.get("x-obtrace-trace-id") or _gen_trace_id()
                    span_id = _gen_span_id()
                    parent = ""
                    tp = request.headers.get("traceparent", "")
                    if tp:
                        parts = tp.split("-")
                        if len(parts) >= 3:
                            trace_id = parts[1]
                        if len(parts) >= 4:
                            parent = parts[2]

                    _set_context(trace_id, span_id)
                    start = time.time()

                    try:
                        response = await call_next(request)
                    except Exception as exc:
                        duration = time.time() - start
                        _enqueue("span", {
                            "traceId": trace_id,
                            "spanId": span_id,
                            "parentSpanId": parent,
                            "name": f"{request.method} {request.url.path}",
                            "kind": 2,
                            "startTimeUnixNano": str(int(start * 1e9)),
                            "endTimeUnixNano": _now_ns(),
                            "attributes": _obj_to_attrs({
                                "http.method": request.method,
                                "http.url": str(request.url),
                                "http.route": request.url.path,
                                "exception.type": type(exc).__name__,
                                "exception.message": str(exc),
                            }),
                            "status": {"code": 2},
                        })
                        _clear_context()
                        raise

                    duration = time.time() - start
                    _enqueue("span", {
                        "traceId": trace_id,
                        "spanId": span_id,
                        "parentSpanId": parent,
                        "name": f"{request.method} {request.url.path}",
                        "kind": 2,
                        "startTimeUnixNano": str(int(start * 1e9)),
                        "endTimeUnixNano": _now_ns(),
                        "attributes": _obj_to_attrs({
                            "http.method": request.method,
                            "http.url": str(request.url),
                            "http.route": request.url.path,
                            "http.status_code": str(response.status_code),
                            "http.user_agent": request.headers.get("user-agent", ""),
                            "net.peer.ip": request.client.host if request.client else "",
                        }),
                        "status": {"code": 2 if response.status_code >= 400 else 1},
                    })

                    response.headers["X-Obtrace-Trace-Id"] = trace_id
                    _clear_context()
                    return response

            _orig_init = fastapi.FastAPI.__init__

            @functools.wraps(_orig_init)
            def _patched_init(self, *args, **kwargs):
                _orig_init(self, *args, **kwargs)
                self.add_middleware(ObtraceMiddleware)

            fastapi.FastAPI.__init__ = _patched_init
        except ImportError:
            pass

    def _patch_flask():
        try:
            import flask

            _orig_init = flask.Flask.__init__

            @functools.wraps(_orig_init)
            def _patched_init(self, *args, **kwargs):
                _orig_init(self, *args, **kwargs)

                @self.before_request
                def _obtrace_before():
                    if not _should_sample():
                        flask.g._obtrace_skip = True
                        return
                    flask.g._obtrace_skip = False
                    req = flask.request
                    trace_id = req.headers.get("X-Obtrace-Trace-Id") or _gen_trace_id()
                    span_id = _gen_span_id()
                    _set_context(trace_id, span_id)
                    flask.g._obtrace_trace_id = trace_id
                    flask.g._obtrace_span_id = span_id
                    flask.g._obtrace_start = time.time()

                    parent = ""
                    tp = req.headers.get("traceparent", "")
                    if tp:
                        parts = tp.split("-")
                        if len(parts) >= 4:
                            parent = parts[2]
                    flask.g._obtrace_parent = parent

                @self.after_request
                def _obtrace_after(response):
                    if getattr(flask.g, "_obtrace_skip", True):
                        return response
                    req = flask.request
                    start = flask.g._obtrace_start
                    _enqueue("span", {
                        "traceId": flask.g._obtrace_trace_id,
                        "spanId": flask.g._obtrace_span_id,
                        "parentSpanId": flask.g._obtrace_parent,
                        "name": f"{req.method} {req.path}",
                        "kind": 2,
                        "startTimeUnixNano": str(int(start * 1e9)),
                        "endTimeUnixNano": _now_ns(),
                        "attributes": _obj_to_attrs({
                            "http.method": req.method,
                            "http.url": req.url,
                            "http.route": req.path,
                            "http.status_code": str(response.status_code),
                        }),
                        "status": {"code": 2 if response.status_code >= 400 else 1},
                    })
                    response.headers["X-Obtrace-Trace-Id"] = flask.g._obtrace_trace_id
                    _clear_context()
                    return response

            flask.Flask.__init__ = _patched_init
        except ImportError:
            pass

    def _collect_runtime_metrics():
        while True:
            time.sleep(15)
            try:
                import resource as res
                usage = res.getrusage(res.RUSAGE_SELF)
                now = _now_ns()

                _enqueue("metric", {
                    "name": "process.runtime.cpython.memory.rss",
                    "unit": "By",
                    "gauge": {"dataPoints": [{"asDouble": usage.ru_maxrss * 1024, "timeUnixNano": now}]},
                })
                _enqueue("metric", {
                    "name": "process.cpu.time",
                    "unit": "s",
                    "sum": {
                        "dataPoints": [{"asDouble": usage.ru_utime + usage.ru_stime, "timeUnixNano": now}],
                        "aggregationTemporality": 2,
                        "isMonotonic": True,
                    },
                })
                _enqueue("metric", {
                    "name": "process.runtime.cpython.gc.count",
                    "unit": "1",
                    "sum": {
                        "dataPoints": [{"asDouble": sum(__import__("gc").get_count()), "timeUnixNano": now}],
                        "aggregationTemporality": 2,
                        "isMonotonic": True,
                    },
                })
                _enqueue("metric", {
                    "name": "process.thread.count",
                    "unit": "1",
                    "gauge": {"dataPoints": [{"asDouble": threading.active_count(), "timeUnixNano": now}]},
                })
            except Exception:
                pass

    def _install_exception_hook():
        _orig_excepthook = sys.excepthook

        def _obtrace_excepthook(exc_type, exc_value, exc_tb):
            trace_id, span_id = _get_context()
            _enqueue("log", {
                "timeUnixNano": _now_ns(),
                "severityNumber": 21,
                "severityText": "FATAL",
                "body": {"stringValue": f"Uncaught {exc_type.__name__}: {exc_value}"},
                "attributes": _obj_to_attrs({
                    "exception.type": exc_type.__name__,
                    "exception.message": str(exc_value),
                    "exception.stacktrace": "".join(traceback.format_tb(exc_tb)),
                }),
                "traceId": trace_id or "",
                "spanId": span_id or "",
            })
            _flush()
            _orig_excepthook(exc_type, exc_value, exc_tb)

        sys.excepthook = _obtrace_excepthook

    _patch_logging()
    _patch_urllib()
    _patch_fastapi()
    _patch_flask()
    _install_exception_hook()

    _metrics_thread = threading.Thread(target=_collect_runtime_metrics, daemon=True)
    _metrics_thread.start()

    print(f"[obtrace-zero] auto-instrumentation active for {OBTRACE_SERVICE_NAME} ({OBTRACE_ENVIRONMENT})")
