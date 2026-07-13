FROM alpine:latest
RUN apk add --no-cache chromium nss freetype harfbuzz font-noto-emoji ttf-freefont socat
EXPOSE 9222
COPY chrome-entrypoint.sh /usr/local/bin/chrome-entrypoint.sh
RUN chmod +x /usr/local/bin/chrome-entrypoint.sh
ENTRYPOINT ["/usr/local/bin/chrome-entrypoint.sh"]
CMD ["about:blank"]
