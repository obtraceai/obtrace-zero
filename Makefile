VERSION ?= 0.1.0
REGISTRY ?= ghcr.io/obtrace
OPERATOR_IMAGE ?= $(REGISTRY)/obtrace-zero-operator
AGENT_IMAGE ?= $(REGISTRY)/obtrace-zero-agent
EBPF_IMAGE ?= $(REGISTRY)/obtrace-zero-ebpf
CLI_IMAGE ?= $(REGISTRY)/obtrace-zero-cli

.PHONY: build test lint docker-build docker-push helm-package install uninstall

build:
	CGO_ENABLED=0 go build -ldflags="-s -w" -o bin/obtrace-zero-operator ./cmd/operator
	CGO_ENABLED=0 go build -ldflags="-s -w" -o bin/obtrace-zero ./cmd/cli

test:
	go test ./... -v -race

lint:
	golangci-lint run ./...

docker-build:
	docker build -t $(OPERATOR_IMAGE):$(VERSION) -f Dockerfile .
	docker build -t $(AGENT_IMAGE):$(VERSION) -f Dockerfile.agent .
	docker build -t $(CLI_IMAGE):$(VERSION) -f Dockerfile.cli .

docker-push: docker-build
	docker push $(OPERATOR_IMAGE):$(VERSION)
	docker push $(AGENT_IMAGE):$(VERSION)
	docker push $(CLI_IMAGE):$(VERSION)

helm-package:
	helm package deploy/helm -d dist/

install:
	helm upgrade --install obtrace-zero deploy/helm \
		--namespace obtrace-system \
		--create-namespace

uninstall:
	helm uninstall obtrace-zero --namespace obtrace-system

dev-install: build
	kubectl apply -f deploy/helm/crds/
	./bin/obtrace-zero install --api-key=$(OBTRACE_API_KEY) --ingest=$(OBTRACE_INGEST_URL)

cli-install:
	CGO_ENABLED=0 go build -ldflags="-s -w" -o /usr/local/bin/obtrace-zero ./cmd/cli

generate-crds:
	kubectl apply -f deploy/helm/crds/obtrace-instrumentation.yaml

cross-build:
	GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o bin/obtrace-zero-linux-amd64 ./cmd/cli
	GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o bin/obtrace-zero-linux-arm64 ./cmd/cli
	GOOS=darwin GOARCH=amd64 go build -ldflags="-s -w" -o bin/obtrace-zero-darwin-amd64 ./cmd/cli
	GOOS=darwin GOARCH=arm64 go build -ldflags="-s -w" -o bin/obtrace-zero-darwin-arm64 ./cmd/cli
