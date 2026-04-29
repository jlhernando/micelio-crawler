# Micelio Docker image with V8 Pointer Compression
# Uses platformatic/node-caged for ~50% memory reduction via compressed pointers
#
# NOTE: better-sqlite3 uses V8 native addon API (not N-API) which is
# incompatible with pointer compression. The --db flag should NOT be used
# with this image. Use JSONL output instead.
#
# Build: docker build -t micelio .
# Run:   docker run --rm micelio spider https://example.com -l 100

FROM platformatic/node-caged:25 AS builder

WORKDIR /app

# Install dependencies
COPY package*.json ./
RUN npm ci

# Copy source and build
COPY tsconfig.json ./
COPY src/ ./src/
RUN npm run build

# Production stage
FROM platformatic/node-caged:25

WORKDIR /app

# Install production dependencies only
COPY package*.json ./
RUN npm ci --omit=dev

# Copy built output
COPY --from=builder /app/dist/ ./dist/

# Pointer compression enforces a 4GB cage per V8 isolate
# Cap heap to stay within the cage
ENV NODE_OPTIONS="--max-old-space-size=4096"

ENTRYPOINT ["node", "dist/cli.js"]
