# Stage 1: Build
FROM registry.access.redhat.com/ubi9/go-toolset:1.23 AS builder

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o airgap ./cmd/airgap

# Stage 2: Runtime
FROM registry.access.redhat.com/ubi9/ubi-minimal:latest

RUN microdnf install -y createrepo_c && microdnf clean all

COPY --from=builder /build/airgap /usr/local/bin/airgap

RUN mkdir -p /var/lib/airgap /etc/airgap
VOLUME ["/var/lib/airgap", "/etc/airgap"]

EXPOSE 8080

USER 1001

ENTRYPOINT ["/usr/local/bin/airgap"]
CMD ["serve"]
