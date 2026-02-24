# Stage 1: Build
FROM registry.access.redhat.com/ubi9/go-toolset:1.23 AS builder
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_TIME=unknown

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.buildTime=${BUILD_TIME}" \
    -o airgap ./cmd/airgap

# Stage 2: Runtime
FROM registry.access.redhat.com/ubi9/ubi-minimal:latest

RUN set -eux; \
    if microdnf install -y createrepo_c; then \
      echo "Installed createrepo_c"; \
    else \
      echo "createrepo_c package unavailable in UBI minimal repositories; continuing without it"; \
    fi; \
    microdnf clean all

COPY --from=builder /build/airgap /usr/local/bin/airgap

RUN mkdir -p /var/lib/airgap /etc/airgap \
    && chown -R 1001:0 /var/lib/airgap /etc/airgap \
    && chmod -R g=u /var/lib/airgap /etc/airgap
VOLUME ["/var/lib/airgap", "/etc/airgap"]

EXPOSE 8080

USER 1001

ENTRYPOINT ["/usr/local/bin/airgap"]
CMD ["serve"]
