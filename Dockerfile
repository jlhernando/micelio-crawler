# Micelio: Go crawler binary with the embedded Svelte dashboard.
#
# Build:  docker build -t micelio .
#         docker build -t micelio --build-arg WITH_CHROMIUM=true .   # JS rendering
# Run UI: the dashboard listens on all interfaces inside the container, so it
#         refuses to start without a token:
#           export MICELIO_AUTH_TOKEN="$(openssl rand -hex 32)"
#           docker run --rm -p 127.0.0.1:3100:3100 -e MICELIO_AUTH_TOKEN \
#             -v micelio-data:/home/micelio/.micelio micelio
#         then open http://localhost:3100 and enter the token once.
# CLI:    docker run --rm micelio crawl https://example.com --max-pages 100
#
# The legacy TypeScript crawler in src/ is not part of this image.
#
# Base images are pinned by digest (multi-arch index, checked 2026-11-06), so
# a rebuild cannot pick up different layers behind the same tag. To update:
# look up the tag's new index digest (docker buildx imagetools inspect
# node:24-alpine) and replace the @sha256 part; Dependabot ("docker" in
# .github/dependabot.yml) proposes these updates.

# ---- Stage 1: dashboard (Vite build -> /src/dist/dashboard) ----
FROM node:24-alpine@sha256:a34ab6d7f6f4813f4d0c5469d74685604b962fbdbf5793a7bc7b4998ecb867c8 AS dashboard
WORKDIR /src/dashboard
COPY dashboard/package.json dashboard/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY dashboard/ ./
RUN npm run build

# ---- Stage 2: Go binary with the dashboard embedded ----
FROM golang:1.26-alpine@sha256:87c65a688975c0b1d4585d204874c45ca78354670e3a5f11368c5a492303d696 AS build
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
FROM alpine:3.22@sha256:bfe7ceb9f09ec0566c48fb93356e45a47a00be5a1dd14fa5b909e7709c66b034
# JS rendering (--js) needs a Chrome/Chromium binary. It is optional
# because it adds ~300 MB to the image.
ARG WITH_CHROMIUM=false
RUN apk add --no-cache ca-certificates tzdata \
    && if [ "$WITH_CHROMIUM" = "true" ]; then apk add --no-cache chromium nss freetype harfbuzz ttf-freefont; fi \
    && adduser -D -h /home/micelio micelio \
    && install -d -o micelio -g micelio -m 700 /home/micelio/.micelio
COPY --from=build /out/micelio /usr/local/bin/micelio
USER micelio
WORKDIR /home/micelio
# UI state (crawl history, settings) lives in ~/.micelio: mount a volume there.
# The directory is created above owned by micelio, so a fresh named or
# anonymous volume starts writable for the non-root user (Docker copies the
# mount point's owner). A bind mount must be writable by the micelio user (uid 1000).
VOLUME ["/home/micelio/.micelio"]
EXPOSE 3100
ENTRYPOINT ["micelio"]
# Needs MICELIO_AUTH_TOKEN (see above); micelio ui exits with an error without it.
CMD ["ui", "--host", "0.0.0.0", "--port", "3100"]
