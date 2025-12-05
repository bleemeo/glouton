#
# To use it, build your image:
# docker build webui --target jsdist --output api/assets/
#

FROM node:24 AS base_builder
ENV PNPM_HOME="/usr/local/share/pnpm"
ENV PATH="$PNPM_HOME:$PATH"
RUN wget -qO- https://get.pnpm.io/install.sh | ENV="$HOME/.shrc" SHELL="$(which sh)" sh -

FROM base_builder AS builder

WORKDIR /app

COPY package.json pnpm-lock.yaml ./
RUN --mount=type=cache,target=/usr/local/share/pnpm/store \
    pnpm install --frozen-lockfile --ignore-scripts

COPY . .

RUN pnpm run build
RUN pnpm run deploy

FROM scratch AS jsdist

COPY --from=builder /api/assets/ /