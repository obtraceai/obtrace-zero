# obtrace-zero

Zero-touch auto-instrumentation operator for Kubernetes. One command, full cluster observability — no code changes required.

## Scope

- Kubernetes operator with mutating webhook for automatic Pod instrumentation
- Language detection from container image, command, args, and labels
- SDK injection for interpreted languages (Node.js, Python, Java, .NET, PHP, Ruby)
- eBPF sidecar for compiled languages (Go, Rust, C++) and unknown workloads
- Hybrid mode combining SDK + eBPF for maximum coverage
- CLI for installation, discovery, and management
- Declarative CRD for GitOps workflows (ArgoCD, Flux)
- Direct OTLP JSON export to ingest-edge — no collector required

## Design Principle

Obtrace Zero follows the same thin-client philosophy as the Obtrace SDKs. The language agents are single-file loaders with zero external dependencies. All policy logic, scrubbing, and normalization happen server-side in the ingest pipeline.

## Install

### CLI

```bash
curl -fsSL https://github.com/obtraceai/obtrace-zero/releases/latest/download/obtrace-zero-$(uname -s | tr '[:upper:]' '[:lower:]')-$(uname -m) \
  -o /usr/local/bin/obtrace-zero && chmod +x /usr/local/bin/obtrace-zero
```

### Operator (via CLI)

```bash
obtrace-zero install --api-key=obt_live_xxx
```

### Operator (via Helm)

```bash
helm upgrade --install obtrace-zero deploy/helm \
  --namespace obtrace-system --create-namespace \
  --set config.apiKey=obt_live_xxx
```

## Quickstart

### 1. Discover what would be instrumented

```bash
obtrace-zero discover
```

```
NAMESPACE    NAME              KIND         LANGUAGE  FRAMEWORK  STRATEGY  IMAGE
production   checkout-api      Deployment   nodejs    nextjs     sdk       node:20-alpine
production   payment-svc       Deployment   java      spring     sdk       eclipse-temurin:21
production   api-gateway       Deployment   go                   ebpf      my-registry/gateway:v2
```

### 2. Install the operator

```bash
obtrace-zero install --api-key=obt_live_xxx
```

### 3. Restart workloads to trigger instrumentation

```bash
kubectl rollout restart deployment -n production
```

### 4. Verify

```bash
obtrace-zero status
```

## Supported Languages

| Language | Detection | Strategy | Framework Detection |
|----------|-----------|----------|-------------------|
| Node.js | `node`, `bun`, `deno`, `npm` | SDK (`NODE_OPTIONS=--require`) | Express, NestJS, Next.js, Elysia |
| Python | `python`, `uvicorn`, `gunicorn` | SDK (`PYTHONSTARTUP`) | FastAPI, Flask, Django |
| Java | `openjdk`, `temurin`, `corretto` | SDK (`JAVA_TOOL_OPTIONS=-javaagent`) | Spring, Quarkus, Micronaut |
| .NET | `dotnet`, `aspnet` | SDK (`DOTNET_STARTUP_HOOKS`) | ASP.NET Core |
| PHP | `php`, `laravel`, `symfony` | SDK (`auto_prepend_file`) | Laravel, Symfony, WordPress |
| Ruby | `ruby`, `rails`, `puma` | SDK (`RUBYOPT=-r`) | Rails, Sidekiq |
| Go | `golang`, distroless | eBPF sidecar | — |
| Rust | `rust` | eBPF sidecar | — |

## Strategies

| Strategy | Description |
|----------|-------------|
| `auto` (default) | SDK for interpreted languages, eBPF for compiled |
| `sdk` | Force SDK injection on all workloads |
| `ebpf` | Force eBPF sidecar on all workloads |
| `hybrid` | Both SDK + eBPF simultaneously |
| `disable` | Turn off instrumentation |

## Configuration (CRD)

```yaml
apiVersion: obtrace.io/v1alpha1
kind: ObtraceInstrumentation
metadata:
  name: obtrace-production
spec:
  apiKeySecretRef:
    name: obtrace-credentials
    key: api-key
  ingestEndpoint: "https://ingest.obtrace.io"
  strategy: "auto"
  namespaces: ["production", "staging"]
  sampling:
    traceRatio: 0.5
  languageHints:
    "api-gateway": "go"
```

## CLI Commands

| Command | Description |
|---------|-------------|
| `obtrace-zero install` | Deploy operator to cluster |
| `obtrace-zero uninstall` | Remove operator from cluster |
| `obtrace-zero discover` | Dry-run: show what would be instrumented |
| `obtrace-zero instrument` | Create instrumentation config for a namespace |
| `obtrace-zero status` | Show operator and instrumented Pods |
| `obtrace-zero version` | Print version |

## Build

```bash
make build        # Compile operator + CLI
make test         # Run tests with race detector
make docker-build # Build all Docker images
make cross-build  # CLI for linux/darwin amd64/arm64
```

## Production Hardening

1. Use `apiKeySecretRef` instead of plain `apiKey` in the CRD
2. Enable cert-manager for webhook TLS certificates
3. Set namespace-scoped instrumentation rather than cluster-wide
4. Configure sampling ratios for high-traffic services
5. Monitor operator health via `/healthz` and `/readyz` endpoints
6. Use `obtrace.io/exclude=true` label on sensitive namespaces

## Troubleshooting

- Pod not instrumented → check `kubectl get oti` and operator logs
- Telemetry not arriving → check `kubectl logs <pod>` for `[obtrace-zero]` message
- Wrong language detected → use `obtrace.io/language` label or `languageHints` in CRD
- eBPF denied → check PodSecurityPolicy/PodSecurityStandard for capability restrictions

## Documentation

- [Overview](https://docs.obtrace.io/docs/obtrace-zero)
- [How it Works](https://docs.obtrace.io/docs/obtrace-zero/how-it-works)
- [Quickstart](https://docs.obtrace.io/docs/obtrace-zero/quickstart)
- [Strategies](https://docs.obtrace.io/docs/obtrace-zero/strategies)
- [Language Agents](https://docs.obtrace.io/docs/obtrace-zero/language-agents)
- [eBPF Deep Dive](https://docs.obtrace.io/docs/obtrace-zero/ebpf)
- [CLI Reference](https://docs.obtrace.io/docs/obtrace-zero/cli-reference)
- [CRD Reference](https://docs.obtrace.io/docs/obtrace-zero/crd-reference)
- [Troubleshooting](https://docs.obtrace.io/docs/obtrace-zero/troubleshooting)
