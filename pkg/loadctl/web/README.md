# lenny-loadctl web UI

HTMX + Plotly UI served by `cmd/lenny-loadctl`. Wave 6 minimum: the index page surfaces the run catalogue and links to the per-run HTML report uploaded to object storage; the live-metrics page that uses the WebSocket telemetry channel is a Wave 6 follow-up.

The UI is intentionally small. Every dynamic surface either round-trips through the REST API in §12.12 or opens a WebSocket against `/api/v1/runs/{id}/metrics:stream`. There is no build pipeline.

## Layout

```
web/
├── README.md            this file
├── index.html           Wave 6 catalogue page (served from / by loadctl)
├── runs/
│   └── detail.html      per-run live view (Wave 6 follow-up)
└── assets/
    └── style.css        the shared stylesheet
```

Wave 6 ships the index from the loadctl binary as an inlined string in `pkg/loadctl/server.go`. The standalone files under `web/` are the editable source the binary embeds; a Wave 6 follow-up adds a `go:embed` directive so the binary picks up edits at build time.
