FROM golang:1.26

RUN apt-get update -qq && \
    apt-get install -yqq --no-install-recommends \
        curl chromium docker.io ffmpeg && \
    curl -fsSL https://deb.nodesource.com/setup_22.x | bash - && \
    apt-get install -yqq --no-install-recommends nodejs && \
    apt-get clean && rm -rf /var/lib/apt/lists/*

WORKDIR /app
