FROM alpine:latest
# nss-tools supplies certutil, which the entrypoint uses to load any CAs the
# image was built with into Chrome's own NSS trust store.
# dbus supplies dbus-daemon: Chromium probes the bus for a password store and
# a battery, and with no bus at all each probe fails and logs an error.
# chromium-swiftshader supplies libvk_swiftshader.so, the software Vulkan
# driver Chromium looks for when there is no GPU. Without it Chromium still
# runs, but every start logs "Couldn't load Vulkan"/"Found no drivers!" and
# WebGL contexts come back null, so a page that renders through WebGL shows
# the agent nothing.
RUN apk add --no-cache chromium chromium-swiftshader nss nss-tools freetype harfbuzz font-noto-emoji ttf-freefont socat dbus
EXPOSE 9222
COPY chrome-entrypoint.sh /usr/local/bin/chrome-entrypoint.sh
RUN chmod +x /usr/local/bin/chrome-entrypoint.sh
ENTRYPOINT ["/usr/local/bin/chrome-entrypoint.sh"]
CMD ["about:blank"]
