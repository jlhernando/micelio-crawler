# Micelio: Go crawler binary with the embedded Svelte dashboard.
#
# Build:  docker build -t micelio .
#         docker build -t micelio --build-arg WITH_CHROMIUM=true .   # JS rendering
# Run UI: docker run --rm -p 3100:3100 -v micelio-data:/home/micelio/.micelio micelio
# CLI:    docker run --rm micelio crawl https://example.com --max-pages 100
#
# The legacy TypeScript crawler in src/ is not part of this image.

# ---- Stage 1: dashboard (Vite build -> /src/dist/dashboard) ----
FROM node:24-alpine AS dashboard
WORKDIR /src/dashboard
COPY dashboard/package.json dashboard/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY dashboard/ ./
RUN npm run build

# ---- Stage 2: Go binary with the dashboard embedded ----
FROM golang:1.26-alpine AS build
WORKDIR /src
ENV CGO_ENABLED=0
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ ./cmd/
COPY internal/ ./internal/
COPY data/ ./data/
# Same as `make build-dashboard`: replace the embedded assets with the fresh build.
COPY --from=dashboard /src/dist/dashboard/ /tmp/dashboard/
RUN rm -rf cmd/micelio/dashboard/dist && mkdir -p cmd/micelio/dashboard/dist \
    && cp -r /tmp/dashboard/. cmd/micelio/dashboard/dist/
ARG VERSION=docker
RUN go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/micelio ./cmd/micelio

# ---- Stage 3: runtime ----
FROM alpine:3.22
# JS rendering (--js) needs a Chrome/Chromium binary. It is optional
# because it adds ~300 MB to the image.
ARG WITH_CHROMIUM=false
RUN apk add --no-cache ca-certificates tzdata \
    && if [ "$WITH_CHROMIUM" = "true" ]; then apk add --no-cache chromium nss freetype harfbuzz ttf-freefont; fi \
    && adduser -D -h /home/micelio micelio
COPY --from=build /out/micelio /usr/local/bin/micelio
USER micelio
WORKDIR /home/micelio
# UI state (crawl history, settings) lives in ~/.micelio: mount a volume there.
VOLUME ["/home/micelio/.micelio"]
EXPOSE 3100
ENTRYPOINT ["micelio"]
CMD ["ui", "--host", "0.0.0.0", "--port", "3100"]
