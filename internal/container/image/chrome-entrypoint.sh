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
    "$@"
