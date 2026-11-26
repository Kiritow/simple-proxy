# syntax=docker/dockerfile:1

# simple-proxy and nginx in one image: the console on port 81 manages the
# nginx serving ports 80 and 443, with acme.sh for certificates. Sites,
# certificates and CAs are kept in the volume /data. See "Docker" in
# README.md.
#
#   docker build -t simple-proxy .

ARG NGINX_IMAGE=docker.io/library/nginx:stable

# The UI is built from web/ rather than taken from internal/web/dist, so
# that the image always matches the sources.
FROM --platform=$BUILDPLATFORM docker.io/library/node:24-alpine AS ui
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci --no-fund --no-audit
COPY web/ ./
# Writes /src/internal/web/dist.
RUN npm run build

FROM --platform=$BUILDPLATFORM docker.io/library/golang:1.27-alpine AS server
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
COPY --from=ui /src/internal/web/dist internal/web/dist
# The same flags as build.sh: a static, stripped binary.
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags='-s -w' -o /out/simple-proxy .

FROM ${NGINX_IMAGE}
ARG ACME_SH_VERSION=3.1.7

# tini reaps the processes of acme.sh and forwards signals. The default
# server of the nginx image would clash with the catch-all servers that
# simple-proxy generates.
RUN apt-get update \
    && apt-get install -y --no-install-recommends tini \
    && rm -rf /var/lib/apt/lists/* \
    && rm /etc/nginx/conf.d/default.conf \
    && mkdir -p /opt/acme.sh \
    && curl -fsSL "https://github.com/acmesh-official/acme.sh/archive/refs/tags/v${ACME_SH_VERSION}.tar.gz" \
        | tar -xz -C /opt/acme.sh --strip-components=1 --wildcards \
            '*/acme.sh' '*/dnsapi' '*/deploy' '*/notify' \
    && /opt/acme.sh/acme.sh --version

# acme.sh runs from the image, and keeps its account and certificates in the
# volume. ACME_SERVER is its certificate authority: letsencrypt, zerossl
# (which needs an account email), or the URL of an ACME directory.
ENV LE_WORKING_DIR=/opt/acme.sh \
    LE_CONFIG_HOME=/data/acme.sh \
    ACME_SERVER=letsencrypt

COPY --from=server /out/simple-proxy /usr/local/bin/simple-proxy
COPY --chmod=0755 docker/entrypoint.sh /usr/local/bin/docker-entrypoint

VOLUME /data
EXPOSE 80 443 81
STOPSIGNAL SIGTERM
ENTRYPOINT ["tini", "--", "docker-entrypoint"]
# Flags for simple-proxy, such as -addr 127.0.0.1:81.
CMD []
