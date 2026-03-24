# obtrace-zero Ruby Auto-Instrumentation Loader
#
# Injected via RUBYOPT="-r /obtrace/obtrace_loader"
# Instruments: Rack, Rails, Net::HTTP, Faraday

require 'json'
require 'net/http'
require 'uri'
require 'securerandom'
require 'thread'

module ObtraceZero
  OBTRACE_API_KEY = ENV['OBTRACE_API_KEY'] || ''
  OBTRACE_INGEST_URL = ENV['OBTRACE_INGEST_URL'] || ''
  OBTRACE_SERVICE_NAME = ENV['OBTRACE_SERVICE_NAME'] || 'unknown'
  OBTRACE_ENVIRONMENT = ENV['OBTRACE_ENVIRONMENT'] || 'unknown'
  OBTRACE_POD_NAME = ENV['OBTRACE_POD_NAME'] || ''
  OBTRACE_POD_NAMESPACE = ENV['OBTRACE_POD_NAMESPACE'] || ''
  OBTRACE_NODE_NAME = ENV['OBTRACE_NODE_NAME'] || ''
  SAMPLE_RATIO = (ENV['OBTRACE_TRACE_SAMPLE_RATIO'] || '1.0').to_f

  @queue = []
  @mutex = Mutex.new
  @max_queue = 500
  @current_trace_id = nil
  @current_span_id = nil

  RESOURCE_ATTRS = {
    'service.name' => OBTRACE_SERVICE_NAME,
    'deployment.environment' => OBTRACE_ENVIRONMENT,
    'k8s.pod.name' => OBTRACE_POD_NAME,
    'k8s.namespace.name' => OBTRACE_POD_NAMESPACE,
    'k8s.node.name' => OBTRACE_NODE_NAME,
    'telemetry.sdk.name' => 'obtrace-zero',
    'telemetry.sdk.language' => 'ruby',
    'process.runtime.name' => 'ruby',
    'process.runtime.version' => RUBY_VERSION,
    'process.pid' => Process.pid.to_s,
  }.freeze

  class << self
    attr_accessor :current_trace_id, :current_span_id

    def enabled?
      !OBTRACE_API_KEY.empty? && !OBTRACE_INGEST_URL.empty?
    end

    def should_sample?
      rand < SAMPLE_RATIO
    end

    def gen_trace_id
      SecureRandom.hex(16)
    end

    def gen_span_id
      SecureRandom.hex(8)
    end

    def now_ns
      (Process.clock_gettime(Process::CLOCK_REALTIME, :nanosecond)).to_s
    end

    def enqueue(type, payload)
      @mutex.synchronize do
        @queue.shift if @queue.size >= @max_queue
        @queue << { type: type, payload: payload }
      end
    end

    def flush
      batch = @mutex.synchronize { @queue.dup.tap { @queue.clear } }
      return if batch.empty?

      spans = batch.select { |b| b[:type] == :span }.map { |b| b[:payload] }
      logs = batch.select { |b| b[:type] == :log }.map { |b| b[:payload] }
      metrics = batch.select { |b| b[:type] == :metric }.map { |b| b[:payload] }

      resource = { attributes: build_attrs(RESOURCE_ATTRS) }

      if spans.any?
        send_telemetry('/otlp/v1/traces', {
          resourceSpans: [{
            resource: resource,
            scopeSpans: [{ scope: { name: 'obtrace-zero-ruby' }, spans: spans }],
          }],
        })
      end

      if logs.any?
        send_telemetry('/otlp/v1/logs', {
          resourceLogs: [{
            resource: resource,
            scopeLogs: [{ scope: { name: 'obtrace-zero-ruby' }, logRecords: logs }],
          }],
        })
      end

      if metrics.any?
        send_telemetry('/otlp/v1/metrics', {
          resourceMetrics: [{
            resource: resource,
            scopeMetrics: [{ scope: { name: 'obtrace-zero-ruby' }, metrics: metrics }],
          }],
        })
      end
    end

    def build_attrs(hash)
      hash.reject { |_, v| v.nil? || v.empty? }
          .map { |k, v| { key: k, value: { stringValue: v.to_s } } }
    end

    private

    def send_telemetry(path, body)
      uri = URI.parse("#{OBTRACE_INGEST_URL.chomp('/')}#{path}")
      http = Net::HTTP.new(uri.host, uri.port)
      http.use_ssl = uri.scheme == 'https'
      http.open_timeout = 5
      http.read_timeout = 5

      req = Net::HTTP::Post.new(uri.path)
      req['Content-Type'] = 'application/json'
      req['X-API-Key'] = OBTRACE_API_KEY
      req['X-Obtrace-Source'] = 'zero-agent-ruby'
      req.body = JSON.generate(body)

      http.request(req)
    rescue StandardError
      nil
    end
  end

  class RackMiddleware
    def initialize(app)
      @app = app
    end

    def call(env)
      return @app.call(env) unless ObtraceZero.should_sample?

      trace_id = extract_trace_id(env) || ObtraceZero.gen_trace_id
      span_id = ObtraceZero.gen_span_id
      parent_span_id = extract_parent_span_id(env)

      ObtraceZero.current_trace_id = trace_id
      ObtraceZero.current_span_id = span_id

      start_ns = Process.clock_gettime(Process::CLOCK_REALTIME, :nanosecond)
      status, headers, body = @app.call(env)
      end_ns = Process.clock_gettime(Process::CLOCK_REALTIME, :nanosecond)

      method = env['REQUEST_METHOD']
      path = env['PATH_INFO']

      ObtraceZero.enqueue(:span, {
        traceId: trace_id,
        spanId: span_id,
        parentSpanId: parent_span_id,
        name: "#{method} #{path}",
        kind: 2,
        startTimeUnixNano: start_ns.to_s,
        endTimeUnixNano: end_ns.to_s,
        attributes: ObtraceZero.build_attrs({
          'http.method' => method,
          'http.url' => env['REQUEST_URI'] || path,
          'http.route' => path,
          'http.status_code' => status.to_s,
          'http.user_agent' => env['HTTP_USER_AGENT'] || '',
          'net.peer.ip' => env['REMOTE_ADDR'] || '',
        }),
        status: { code: status.to_i >= 400 ? 2 : 1 },
      })

      headers['X-Obtrace-Trace-Id'] = trace_id
      ObtraceZero.current_trace_id = nil
      ObtraceZero.current_span_id = nil

      [status, headers, body]
    rescue StandardError => e
      ObtraceZero.enqueue(:log, {
        timeUnixNano: ObtraceZero.now_ns,
        severityNumber: 21,
        severityText: 'FATAL',
        body: { stringValue: "#{e.class}: #{e.message}" },
        attributes: ObtraceZero.build_attrs({
          'exception.type' => e.class.name,
          'exception.message' => e.message,
          'exception.stacktrace' => e.backtrace&.first(20)&.join("\n") || '',
        }),
        traceId: trace_id || '',
        spanId: span_id || '',
      })
      raise
    end

    private

    def extract_trace_id(env)
      tp = env['HTTP_TRACEPARENT']
      return tp.split('-')[1] if tp && tp.split('-').size >= 3
      env['HTTP_X_OBTRACE_TRACE_ID']
    end

    def extract_parent_span_id(env)
      tp = env['HTTP_TRACEPARENT']
      return tp.split('-')[2] if tp && tp.split('-').size >= 4
      ''
    end
  end
end

if ObtraceZero.enabled?
  Thread.new do
    loop do
      sleep 2
      ObtraceZero.flush rescue nil
    end
  end

  at_exit { ObtraceZero.flush }

  if defined?(Rails)
    Rails.application.config.middleware.insert(0, ObtraceZero::RackMiddleware)
  end

  $stderr.puts "[obtrace-zero] auto-instrumentation active for #{ObtraceZero::OBTRACE_SERVICE_NAME} (#{ObtraceZero::OBTRACE_ENVIRONMENT})"
end
