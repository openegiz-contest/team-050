FROM node:24-bookworm-slim@sha256:91acba91b842bec0bf7ea6f5e09c644c7ccad72c4996ed9616d861d1c7cd60d4 AS ui
WORKDIR /ui
COPY team/mine/ui/package*.json ./
RUN npm ci --no-audit --no-fund
COPY team/mine/ui/ ./
RUN npm run build

FROM golang:1.24-bookworm@sha256:1a6d4452c65dea36aac2e2d606b01b4a029ec90cc1ae53890540ce6173ea77ac AS go
WORKDIR /src
COPY team/mine/go.mod team/mine/go.sum ./
RUN go mod download
COPY team/mine/ ./
ENV GOMAXPROCS=2 GOMEMLIMIT=512MiB
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -p 2 -trimpath -o /out/mine-engine ./cmd/mine-engine \
 && CGO_ENABLED=0 go build -p 2 -trimpath -o /out/mine-web ./cmd/mine-web \
 && CGO_ENABLED=0 go build -p 2 -trimpath -o /out/mine-setup ./cmd/mine-setup

FROM debian:bookworm-slim@sha256:7ff6ba31527b9e336162fa061746c366c4b2032f4d2cff2078bea43f78efe3f8
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl \
 && rm -rf /var/lib/apt/lists/* \
 && useradd --uid 10001 --create-home mine \
 && mkdir -p /app/data && chown mine:mine /app/data
WORKDIR /app
COPY --from=go /out/ /usr/local/bin/
COPY --from=ui /ui/dist/ /app/ui/dist/
COPY team/mine/config/ /app/config/
USER mine
CMD ["mine-web"]
