#
# To use it, build your image:
# docker build webui --target jsdist --output api/assets/
#

FROM node:24 AS base_builder
RUN wget -qO- https://get.pnpm.io/install.sh | ENV="$HOME/.shrc" SHELL="$(which sh)" sh -
ENV PNPM_HOME="/root/.local/share/pnpm"
ENV PATH="$PNPM_HOME:$PATH"

FROM base_builder AS builder

WORKDIR /app

COPY package.json pnpm-lock.yaml ./
RUN --mount=type=cache,target=/root/.local/share/pnpm/store \
    pnpm install --frozen-lockfile --ignore-scripts

COPY . .

RUN pnpm run build
RUN pnpm run deploy

FROM scratch AS jsdist

COPY --from=builder /api/assets/ /