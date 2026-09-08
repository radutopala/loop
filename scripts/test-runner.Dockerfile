# Pinned to the exact patch release in go.mod: the official golang images set
# GOTOOLCHAIN=local, so a runner older than the go directive cannot build the
# module. The floating 1.27 tag also left CI stale, because ensure-test-runner
# keys its cache on this file's hash and never noticed upstream moving.
FROM golang:1.27.1

RUN apt-get update -qq && \
    apt-get install -yqq --no-install-recommends \
        curl chromium docker-cli ffmpeg fluidsynth fluid-soundfont-gm && \
    curl -fsSL https://deb.nodesource.com/setup_24.x | bash - && \
    apt-get install -yqq --no-install-recommends nodejs && \
    apt-get clean && rm -rf /var/lib/apt/lists/*

WORKDIR /app
