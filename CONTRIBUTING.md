# Contributing

## Workflow

1. Fork and clone this repo
2. Create a feature branch from `main`
3. Make changes and validate locally
4. Open a pull request

## Commit Style

Use imperative mood in commit messages:

- `add eBPF DNS probe configuration`
- `fix Python agent thread safety on flush`
- `update Helm chart default resource limits`

## Validation

```bash
make build        # Compile operator and CLI
make test         # Run tests with race detector
make lint         # Run golangci-lint
make docker-build # Verify Docker images build
```

## Pull Requests

- Describe what changed and why
- Reference related issues if applicable
- Ensure CI passes before requesting review

## Security

1. Never commit API keys, secrets, or credentials
2. Never bypass webhook TLS verification
3. Report security issues privately — do not open public issues
