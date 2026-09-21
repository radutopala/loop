#!/bin/sh
# Chrome on Linux does not read /etc/ssl/certs: it trusts the Chrome Root Store
# plus an NSS database at $HOME/.pki/nssdb. Any CA added to this image is
# therefore invisible to it, and every host behind that CA answers with an
# interstitial instead of a page. Load them into NSS before starting.
#
# certutil imports only the FIRST certificate out of a multi-cert PEM, so each
# bundle is split into one file per certificate first. No-op when the image
# carries no extra CAs.
import_cas() {
    command -v certutil >/dev/null 2>&1 || return 0

    certdir="$HOME/.pki/nssdb"
    mkdir -p "$certdir"
    [ -f "$certdir/cert9.db" ] || certutil -N -d "sql:$certdir" --empty-password || return 0

    count=0
    for bundle in /usr/local/share/ca-certificates/*.crt; do
        [ -f "$bundle" ] || continue
        tmp=$(mktemp -d) || continue
        awk -v dir="$tmp" '/BEGIN CERT/{n++} n{print > (dir "/" n ".pem")}' "$bundle"
        for cert in "$tmp"/*.pem; do
            [ -f "$cert" ] || continue
            count=$((count + 1))
            certutil -A -d "sql:$certdir" -t "C,," -n "loop-ca-$count" -i "$cert"
        done
        rm -rf "$tmp"
    done
    [ "$count" -eq 0 ] || echo "chrome-entrypoint: imported $count CA(s) into $certdir" >&2
}

import_cas

# Chromium asks D-Bus which password store to use and where the battery is.
# A container has no bus, so every one of those probes fails and logs, and the
# session-bus address it reads from the environment is empty, which logs again.
# Starting both buses answers the probes instead: nothing is listening behind
# them, so Chromium hears an honest "no" and falls back exactly as before.
start_dbus() {
    command -v dbus-daemon >/dev/null 2>&1 || return 0

    mkdir -p /run/dbus
    dbus-daemon --system --fork 2>/dev/null || return 0

    address=$(dbus-daemon --session --fork --print-address 2>/dev/null) || return 0
    DBUS_SESSION_BUS_ADDRESS="$address"
    export DBUS_SESSION_BUS_ADDRESS
}

start_dbus

# Chrome binds to 127.0.0.1 despite --remote-debugging-address=0.0.0.0 on Alpine.
# Use socat to proxy 0.0.0.0:9222 -> 127.0.0.1:9223 (Chrome on internal port).
socat TCP-LISTEN:9222,fork,reuseaddr,bind=0.0.0.0 TCP:127.0.0.1:9223 &
# --enable-unsafe-swiftshader replaces --disable-gpu/--disable-software-rasterizer:
# those two left the container with no rasterizer at all, so WebGL was null.
# SwiftShader renders it on the CPU instead. The "unsafe" name is about running
# untrusted shader code on the CPU, and it is also what stops Chromium logging
# its deprecation error for the automatic software-WebGL fallback.
#
# --disable-features turns off the two things the container advertises but
# cannot deliver. Dawn goes looking for a Vulkan driver the moment a page asks
# for a WebGPU adapter, finds none and logs it; the on-device model service
# fails to load a backend the moment a page asks whether the built-in AI is
# available, and until it is asked it reports itself as downloadable. Both
# already answered every page with "no", so nothing is lost by not offering
# them, and the log stops filling with each refusal.
exec chromium-browser \
    --no-sandbox \
    --headless=new \
    --enable-unsafe-swiftshader \
    --remote-debugging-port=9223 \
    --remote-allow-origins=* \
    --disable-dev-shm-usage \
    --disable-features=WebGPU,OptimizationGuideOnDeviceModel \
    "$@"
