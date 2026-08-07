FROM mcr.microsoft.com/devcontainers/typescript-node:1-22-bookworm

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
       ca-certificates \
       curl \
       git \
       jq \
       make \
       sqlite3 \
       zip \
    && rm -rf /var/lib/apt/lists/*
