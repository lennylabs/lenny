// SPDX-License-Identifier: MIT

package loadreport

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"time"
)

// Run is the per-run manifest passed to Render. It is a superset of
// the Run shape loadctl exposes; metric series are loaded from the
// Prometheus snapshot and k6 JSON exports referenced by RunID.
type Run struct {
	ID             string
	Branch         string
	Commit         string
	ImageTag       string
	Scale          string
	ClusterRelease string
	StartedAt      time.Time
	CompletedAt    time.Time
	Scenarios      []ScenarioResult
	Resources      ResourceSeries
}

// ScenarioResult is one scenario's outcome.
type ScenarioResult struct {
	Name        string
	Status      string
	Throughput  float64
	ErrorRate   float64
	Latency     Latency
	Errors      []ErrorBucket
}

// Latency is the percentile summary surfaced in the report.
type Latency struct {
	Avg, P50, P90, P95, P99, P999, Max float64
}

// ErrorBucket is one entry in the per-scenario error code histogram.
type ErrorBucket struct {
	Code  string
	Count int64
}

// ResourceSeries holds the time-series the report charts.
type ResourceSeries struct {
	GatewayCPU      []Point
	GatewayMem      []Point
	ControllerCPU   []Point
	DatabaseConn    []Point
	CacheCPU        []Point
	NodeCPU         []Point
	PodCreationRate []Point
}

// Point is one (timestamp, value) sample.
type Point struct {
	T time.Time
	V float64
}

// Render writes a self-contained HTML report for run to out.
func Render(out io.Writer, run *Run) error {
	if run == nil {
		return fmt.Errorf("loadreport: Render requires a non-nil Run")
	}
	data := reportData{
		Run:  run,
		JSON: mustEncode(run),
	}
	return tmpl.Execute(out, data)
}

// RenderBytes returns the rendered HTML as a byte slice.
func RenderBytes(run *Run) ([]byte, error) {
	var buf bytes.Buffer
	if err := Render(&buf, run); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

type reportData struct {
	Run  *Run
	JSON template.JS
}

func mustEncode(v any) template.JS {
	b, err := json.Marshal(v)
	if err != nil {
		// Fallback to empty object; the JS layer surfaces "no data".
		return template.JS(`{}`)
	}
	return template.JS(b)
}

var tmpl = template.Must(template.New("report").Parse(reportTemplate))

const reportTemplate = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8" />
<title>Lenny load report — {{.Run.ID}}</title>
<style>
  body { font-family: -apple-system, system-ui, Helvetica, Arial, sans-serif; max-width: 1200px; margin: 2em auto; padding: 0 1em; color: #1f2933; line-height: 1.5; }
  h1 { font-size: 1.6em; margin-bottom: 0.3em; }
  h2 { font-size: 1.2em; margin-top: 2em; border-bottom: 1px solid #c0b290; padding-bottom: 0.2em; }
  .header-meta { color: #54616e; font-size: 0.9em; }
  table { border-collapse: collapse; width: 100%; margin: 1em 0; }
  th, td { padding: 0.4em 0.6em; text-align: left; border-bottom: 1px solid #d3c8b0; }
  th { background: #fffaf0; font-weight: 600; }
  .pass { color: #1b6f3a; font-weight: 600; }
  .fail { color: #c84a1d; font-weight: 600; }
  .chart { width: 100%; height: 320px; margin: 1em 0; }
  code { background: #fffaf0; padding: 0.1em 0.3em; border-radius: 4px; font-family: SF Mono, Menlo, Consolas, monospace; font-size: 0.85em; }
</style>
<script src="https://cdn.plot.ly/plotly-2.35.2.min.js" charset="utf-8"></script>
</head>
<body>
<h1>Lenny load report — <code>{{.Run.ID}}</code></h1>
<p class="header-meta">
  branch <code>{{.Run.Branch}}</code> · commit <code>{{.Run.Commit}}</code> · image <code>{{.Run.ImageTag}}</code>
  · scale <code>{{.Run.Scale}}</code> · cluster <code>{{.Run.ClusterRelease}}</code>
  · started {{.Run.StartedAt.Format "2006-01-02 15:04:05 MST"}}
  · completed {{.Run.CompletedAt.Format "2006-01-02 15:04:05 MST"}}
</p>

<h2>Scenario results</h2>
<table>
  <thead>
    <tr><th>Scenario</th><th>Status</th><th>Throughput (/s)</th><th>Error rate</th><th>P50 (s)</th><th>P95 (s)</th><th>P99 (s)</th><th>P99.9 (s)</th><th>Max (s)</th></tr>
  </thead>
  <tbody>
    {{range .Run.Scenarios}}
    <tr>
      <td><code>{{.Name}}</code></td>
      <td class="{{if eq .Status "PASS"}}pass{{else}}fail{{end}}">{{.Status}}</td>
      <td>{{printf "%.1f" .Throughput}}</td>
      <td>{{printf "%.3f%%" (.ErrorRate)}}</td>
      <td>{{printf "%.4f" .Latency.P50}}</td>
      <td>{{printf "%.4f" .Latency.P95}}</td>
      <td>{{printf "%.4f" .Latency.P99}}</td>
      <td>{{printf "%.4f" .Latency.P999}}</td>
      <td>{{printf "%.4f" .Latency.Max}}</td>
    </tr>
    {{end}}
  </tbody>
</table>

<h2>Resources</h2>
<div id="gateway-cpu-chart" class="chart"></div>
<div id="gateway-mem-chart" class="chart"></div>
<div id="db-chart" class="chart"></div>
<div id="cache-chart" class="chart"></div>
<div id="node-chart" class="chart"></div>
<div id="pod-creation-chart" class="chart"></div>

<script>
const data = {{.JSON}};

function pointsToTrace(points, name) {
  return {
    x: points.map(p => p.T),
    y: points.map(p => p.V),
    name: name,
    mode: 'lines',
    line: { color: '#b56b1f' }
  };
}

function renderChart(divID, points, title, yLabel) {
  if (!points || points.length === 0) {
    document.getElementById(divID).innerHTML = '<em>no data</em>';
    return;
  }
  Plotly.newPlot(divID, [pointsToTrace(points, title)], {
    title: title,
    margin: { t: 40, r: 20, b: 30, l: 60 },
    yaxis: { title: yLabel },
    xaxis: { type: 'date' },
    paper_bgcolor: '#ffffff',
    plot_bgcolor: '#fffaf0'
  }, { displayModeBar: false });
}

renderChart('gateway-cpu-chart',  data.Resources && data.Resources.GatewayCPU,      'Gateway CPU',           'cores');
renderChart('gateway-mem-chart',  data.Resources && data.Resources.GatewayMem,      'Gateway memory',        'bytes');
renderChart('db-chart',           data.Resources && data.Resources.DatabaseConn,    'Database connections',  'count');
renderChart('cache-chart',        data.Resources && data.Resources.CacheCPU,        'Cache CPU',             'cores');
renderChart('node-chart',         data.Resources && data.Resources.NodeCPU,         'Node CPU',              'cores');
renderChart('pod-creation-chart', data.Resources && data.Resources.PodCreationRate, 'Pod creation rate',     'pods/s');
</script>
</body>
</html>`
