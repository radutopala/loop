#!/bin/sh
# Chrome binds to 127.0.0.1 despite --remote-debugging-address=0.0.0.0 on Alpine.
# Use socat to proxy 0.0.0.0:9222 -> 127.0.0.1:9223 (Chrome on internal port).
socat TCP-LISTEN:9222,fork,reuseaddr,bind=0.0.0.0 TCP:127.0.0.1:9223 &
exec chromium-browser \
    --no-sandbox \
    --disable-gpu \
    --headless=new \
    --remote-debugging-port=9223 \
    --remote-allow-origins=* \
    --disable-dev-shm-usage \
    --disable-software-rasterizer \
    --disable-extensions \
    "$@"
