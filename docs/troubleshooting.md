---
title: Troubleshooting
---
## Permission denied writing LaunchAgents plist

When running `loop daemon:start`, you may see:

```
Error: writing plist: open /Users/<user>/Library/LaunchAgents/com.loop.agent.plist: permission denied
```

This happens when `~/Library/LaunchAgents/` or the plist file is owned by `root` instead of your user. Deleting or writing the plist also requires write permission on the directory itself. Fix both by reclaiming ownership:

```bash
sudo chown $(whoami):staff ~/Library/LaunchAgents
sudo chown $(whoami):staff ~/Library/LaunchAgents/com.loop.agent.plist
```

Then retry `loop daemon:start`.

## Docker build fails with TLS certificate errors

When running `loop serve`, the Docker image build may fail with:

```
E: Failed to fetch https://deb.debian.org/debian/dists/.../Release.gpg  Server certificate verification failed. CAfile: ...
E: Some index files failed to download.
```

This occurs on corporate networks where a proxy or firewall (e.g. Palo Alto, Zscaler, Netskope) performs SSL inspection using a custom CA certificate that Docker containers don't trust.

### Fix: inject corporate CA certificates into the build

1. **Export your corporate CA certificates** from the macOS system keychain:

   ```bash
   security find-certificate -a -p /Library/Keychains/System.keychain > ~/.loop/container/corporate-ca.pem
   ```

2. **Patch `~/.loop/container/Dockerfile`** to inject the certificates before any network operations. Add these lines after each `FROM` line and before any `RUN apt-get` or `RUN curl` commands:

   ```dockerfile
   COPY corporate-ca.pem /usr/local/share/ca-certificates/corporate-ca.crt
   RUN update-ca-certificates
   ```

   For example, the builder stage becomes:

   ```dockerfile
   FROM golang:1.26 AS builder
   COPY corporate-ca.pem /usr/local/share/ca-certificates/corporate-ca.crt
   RUN update-ca-certificates
   ```

   Apply the same pattern to the final stage and to `~/.loop/container/chrome.Dockerfile` (note: `chrome.Dockerfile` is still Alpine — use the `cat … >> /etc/ssl/certs/ca-certificates.crt` form there).

3. **Retry** `loop serve`.

> **Note:** When `loop serve` ships a new version of the embedded container files, your edited `~/.loop/container/Dockerfile` (and `chrome.Dockerfile`) is preserved as `Dockerfile.bkp` (and `chrome.Dockerfile.bkp`) before being overwritten. Diff the `.bkp` against the refreshed file to re-apply your local changes.
