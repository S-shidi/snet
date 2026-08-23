# ---- Stage 1: Build snetd + snetctl ----
FROM golang:1.26-alpine AS builder

RUN apk add --no-cache git

# Cache dependencies
WORKDIR /src
COPY go.mod go.sum ./
ENV GOPROXY=https://goproxy.cn,direct
RUN go mod download

# Build static binaries
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags="-s -w" -o /out/snetd   ./cmd/client/snetd && \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags="-s -w" -o /out/snetctl ./cmd/client/snetctl

# ---- Stage 1b: Build web UI from shared TypeScript ----
FROM node:20-alpine AS webbuilder

WORKDIR /src
COPY desktop/package.json desktop/package-lock.json* ./
RUN npm install --ignore-scripts 2>/dev/null || true

# Copy shared TypeScript source
COPY shared/web/ ./shared/web/
# Copy web adapter and HTML/CSS
COPY desktop/src/web.ts ./desktop/src/web.ts
COPY deploy/docker/web/index.html ./deploy/docker/web/index.html
COPY deploy/docker/web/styles.css ./deploy/docker/web/styles.css

# Bundle TypeScript → single app.js (ES2022 target, IIFE format)
RUN npx esbuild desktop/src/web.ts \
    --bundle --format=iife --target=es2022 \
    --outfile=deploy/docker/web/app.js \
    --define:process.env.NODE_ENV=\"production\" \
    --minify

# ---- Stage 2: Runtime ----
FROM alpine:3.19

RUN apk add --no-cache wireguard-tools curl nginx

COPY --from=builder /out/snetd   /usr/local/bin/snetd
COPY --from=builder /out/snetctl /usr/local/bin/snetctl

RUN install -d -m 700 /data

# Web UI static files (from webbuilder stage)
RUN install -d -m 755 /var/www/snet
COPY --from=webbuilder /src/deploy/docker/web/index.html /var/www/snet/
COPY --from=webbuilder /src/deploy/docker/web/styles.css  /var/www/snet/
COPY --from=webbuilder /src/deploy/docker/web/app.js      /var/www/snet/

# Nginx config
COPY deploy/docker/nginx.conf /etc/nginx/http.d/default.conf

COPY deploy/docker/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

VOLUME /data

# Web UI (nginx)
EXPOSE 8080
# Web UI HTTPS
EXPOSE 8443
# WireGuard 数据面 (每个网络一个端口，从51820起)
EXPOSE 51900-51963/udp

ENTRYPOINT ["/entrypoint.sh"]
