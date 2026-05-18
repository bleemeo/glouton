#
# To use it, build your image:
# docker build webui --target jsdist --output api/assets/
#

FROM node:24 AS base_builder
ENV PNPM_HOME="/pnpm"
ENV PATH="$PNPM_HOME:$PATH"
# Do the pnpm --version to trigger corepack's download of pnpm, so it's stay cached in Docker layers.
RUN corepack enable && pnpm --version

FROM base_builder AS builder

WORKDIR /app

COPY package.json pnpm-lock.yaml ./
RUN --mount=type=cache,id=pnpm,target=/pnpm/store \
    pnpm install --frozen-lockfile --ignore-scripts

COPY . .

RUN pnpm run build
RUN pnpm run deploy

FROM scratch AS jsdist

COPY --from=builder /api/assets/ /
