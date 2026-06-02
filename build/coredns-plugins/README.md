# Dedicated CoreDNS plugins

The dedicated lenny-system CoreDNS instance (spec §13.2 lines 452-536)
embeds two non-standard plugins that stock CoreDNS does not ship:

- `ratelimit` — per-source-IP DNS response throttling. The Corefile
  configures `responses_per_second` (default 10) to blunt
  high-throughput DNS tunneling.
- `filter` — response filtering. The Corefile configures `max_txt_size`
  (default 255) and `block_types` (default `NULL PRIVATE KEY TYPE65534`)
  to drop oversized TXT records and the record types DNS-tunnel
  exfiltration carries.

Both plugins are compiled into the `lenny-coredns` image referenced by
the `coredns.image` Helm value. The image is built from `Dockerfile` in
this directory, which adds the two external plugins to CoreDNS's
`plugin.cfg` (see `plugin.cfg` here) and rebuilds the CoreDNS binary.

## Building the image

```
docker build \
  --build-arg COREDNS_VERSION=v1.11.3 \
  -t ghcr.io/lennylabs/lenny-coredns:1.11.3-lenny1 \
  build/coredns-plugins
```

The Helm chart renders a readiness probe against the CoreDNS health
endpoint (spec §13.2 line 536); a custom image missing either plugin
fails to load the Corefile and the probe keeps the pod out of the
`lenny-agent-dns` Service.

## Plugin sources

The two plugins are external Go modules pinned in `plugin.cfg`. They are
fetched at image-build time by the CoreDNS plugin-generation step. To
vendor a fully air-gapped build, mirror the pinned module versions into
an internal proxy and set `GOPROXY` in the build environment.

| Plugin      | Module                                              |
| ----------- | --------------------------------------------------- |
| `ratelimit` | `github.com/coredns/ratelimit`                      |
| `filter`    | `github.com/milgradesec/filter`                     |
