FROM debian:trixie-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
        chromium fonts-noto-color-emoji fonts-freefont-ttf \
        socat ca-certificates \
    && rm -rf /var/lib/apt/lists/*
EXPOSE 9222
COPY chrome-entrypoint.sh /usr/local/bin/chrome-entrypoint.sh
RUN chmod +x /usr/local/bin/chrome-entrypoint.sh
ENTRYPOINT ["/usr/local/bin/chrome-entrypoint.sh"]
CMD ["about:blank"]
