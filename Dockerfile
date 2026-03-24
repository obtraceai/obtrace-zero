FROM golang:1.24-alpine AS builder
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /obtrace-zero-operator ./cmd/operator
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /obtrace-zero-cli ./cmd/cli

FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /obtrace-zero-operator /obtrace-zero-operator
USER 65532:65532
ENTRYPOINT ["/obtrace-zero-operator"]
