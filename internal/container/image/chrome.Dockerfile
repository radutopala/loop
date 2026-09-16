FROM alpine:latest
# nss-tools supplies certutil, which the entrypoint uses to load any CAs the
# image was built with into Chrome's own NSS trust store.
RUN apk add --no-cache chromium nss nss-tools freetype harfbuzz font-noto-emoji ttf-freefont socat
EXPOSE 9222
COPY chrome-entrypoint.sh /usr/local/bin/chrome-entrypoint.sh
RUN chmod +x /usr/local/bin/chrome-entrypoint.sh
ENTRYPOINT ["/usr/local/bin/chrome-entrypoint.sh"]
CMD ["about:blank"]
