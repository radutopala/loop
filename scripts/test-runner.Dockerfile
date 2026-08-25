FROM golang:1.27

RUN apt-get update -qq && \
    apt-get install -yqq --no-install-recommends \
        curl chromium docker-cli ffmpeg fluidsynth fluid-soundfont-gm && \
    curl -fsSL https://deb.nodesource.com/setup_24.x | bash - && \
    apt-get install -yqq --no-install-recommends nodejs && \
    apt-get clean && rm -rf /var/lib/apt/lists/*

WORKDIR /app
