# Base image for per-user pods. The supervisor runs as PID 1 and forks
# one Node process per workflow run; the SDK is pre-installed into
# /opt/platform so workflow code can `import '@platform/sdk'` without
# its own install step.

FROM docker.io/library/node:20-slim

ENV NODE_ENV=production \
    PNPM_HOME=/usr/local/pnpm \
    PATH=/usr/local/pnpm:$PATH

RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl \
 && rm -rf /var/lib/apt/lists/* \
 && corepack enable

WORKDIR /opt/platform

COPY sdk/package.json sdk/tsconfig.json /opt/platform/sdk/
COPY sdk/src /opt/platform/sdk/src
RUN cd sdk && pnpm install --prod=false --frozen-lockfile || pnpm install --prod=false \
 && pnpm exec tsc \
 && pnpm prune --prod

COPY sandbox/package.json sandbox/tsconfig.json /opt/platform/supervisor/
COPY sandbox/src /opt/platform/supervisor/src
RUN cd supervisor && pnpm install --prod=false \
 && pnpm exec tsc

USER node
WORKDIR /home/node

EXPOSE 50051
ENTRYPOINT ["node", "/opt/platform/supervisor/dist/supervisor.js"]
