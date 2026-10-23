package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"sort"
	"strings"
)

func renderE2EReportHTML(rep E2EReport) []byte {
	var b bytes.Buffer
	status := "FAILED"
	statusClass := "fail"
	if rep.OK {
		status = "PASSED"
		statusClass = "ok"
	}
	b.WriteString(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8"/>
<meta name="viewport" content="width=device-width, initial-scale=1"/>
<title>E2E Report `)
	b.WriteString(html.EscapeString(rep.ID))
	b.WriteString(`</title>
<style>
:root { color-scheme: light dark; --bg:#0f1419; --fg:#e7ecf1; --muted:#9aa7b5; --line:#2a3441; --ok:#3d9a6a; --fail:#c45c5c; --card:#171e26; }
@media (prefers-color-scheme: light) {
  :root { --bg:#f6f8fa; --fg:#1a2330; --muted:#5b6b7c; --line:#d5dde6; --ok:#1f7a4c; --fail:#b42318; --card:#fff; }
}
body { margin:0; font:15px/1.45 system-ui,sans-serif; background:var(--bg); color:var(--fg); }
main { max-width:960px; margin:0 auto; padding:1.5rem 1.25rem 3rem; }
h1 { margin:0 0 .35rem; font-size:1.45rem; }
.muted { color:var(--muted); }
.badge { display:inline-block; padding:.15rem .55rem; border-radius:4px; font-weight:600; font-size:.8rem; }
.badge.ok { background:color-mix(in srgb, var(--ok) 22%, transparent); color:var(--ok); }
.badge.fail { background:color-mix(in srgb, var(--fail) 22%, transparent); color:var(--fail); }
.summary { display:grid; grid-template-columns:repeat(auto-fit,minmax(8rem,1fr)); gap:.65rem; margin:1rem 0 1.4rem; }
.summary div { background:var(--card); border:1px solid var(--line); border-radius:8px; padding:.65rem .75rem; }
.summary dt { font-size:.72rem; text-transform:uppercase; letter-spacing:.04em; color:var(--muted); }
.summary dd { margin:.15rem 0 0; font-size:1.15rem; font-weight:600; }
article { background:var(--card); border:1px solid var(--line); border-radius:10px; padding:1rem 1.05rem; margin:0 0 .85rem; }
article.passed { border-left:3px solid var(--ok); }
article.failed { border-left:3px solid var(--fail); }
article h2 { margin:0 0 .35rem; font-size:1.05rem; }
.meta { font-size:.82rem; color:var(--muted); margin-bottom:.5rem; }
.bar { display:flex; height:8px; border-radius:99px; overflow:hidden; background:var(--line); margin:.45rem 0; }
.bar span { display:block; height:100%; }
.bar .a { background:var(--ok); }
.bar .d { background:var(--fail); }
.bar .o { background:var(--muted); }
table { width:100%; border-collapse:collapse; font-size:.82rem; margin-top:.4rem; }
th,td { text-align:left; padding:.3rem .4rem; border-bottom:1px solid var(--line); vertical-align:top; }
code { font-size:.8rem; }
pre { background:color-mix(in srgb, var(--line) 45%, transparent); padding:.65rem; border-radius:6px; overflow:auto; font-size:.75rem; }
ul.steps { margin:.35rem 0 0; padding-left:1.1rem; font-size:.85rem; }
.steps .ok { color:var(--ok); }
.steps .bad { color:var(--fail); }
</style>
</head>
<body>
<main>
`)
	fmt.Fprintf(&b, `<h1>E2E automation report <span class="badge %s">%s</span></h1>`, statusClass, status)
	fmt.Fprintf(&b, `<p class="muted">id <code>%s</code> · started %s · ended %s · %s</p>`,
		html.EscapeString(rep.ID),
		html.EscapeString(rep.StartedAt.UTC().Format("2006-01-02 15:04:05 MST")),
		html.EscapeString(rep.EndedAt.UTC().Format("2006-01-02 15:04:05 MST")),
		html.EscapeString(rep.Duration),
	)
	b.WriteString(`<dl class="summary">`)
	fmt.Fprintf(&b, `<div><dt>Passed</dt><dd>%d / %d</dd></div>`, rep.Summary.Passed, rep.Summary.TotalSetups)
	fmt.Fprintf(&b, `<div><dt>Failed</dt><dd>%d</dd></div>`, rep.Summary.Failed)
	fmt.Fprintf(&b, `<div><dt>Allowed</dt><dd>%d</dd></div>`, rep.Summary.Allowed)
	fmt.Fprintf(&b, `<div><dt>Rate limited</dt><dd>%d</dd></div>`, rep.Summary.RateLimited)
	fmt.Fprintf(&b, `<div><dt>Vegeta jobs</dt><dd>%d</dd></div>`, rep.Summary.VegetaJobs)
	b.WriteString(`</dl>`)

	for _, s := range rep.Setups {
		cls := s.Status
		if cls != "passed" && cls != "failed" {
			cls = "failed"
		}
		fmt.Fprintf(&b, `<article class="%s">`, html.EscapeString(cls))
		fmt.Fprintf(&b, `<h2>%s <span class="badge %s">%s</span></h2>`,
			html.EscapeString(s.Name), html.EscapeString(cls), html.EscapeString(s.Status))
		fmt.Fprintf(&b, `<p class="meta"><code>%s</code> · %s · %s · %dms</p>`,
			html.EscapeString(s.SetupID), html.EscapeString(s.Group), html.EscapeString(s.Driver), s.DurationMs)
		if s.Description != "" {
			fmt.Fprintf(&b, `<p>%s</p>`, html.EscapeString(s.Description))
		}
		if s.Message != "" {
			fmt.Fprintf(&b, `<p><strong>Result:</strong> %s</p>`, html.EscapeString(s.Message))
		}
		t := s.Traffic
		total := t.Allowed + t.RateLimited + t.Other
		if total < 1 {
			total = 1
		}
		fmt.Fprintf(&b, `<div class="bar"><span class="a" style="width:%.1f%%"></span><span class="d" style="width:%.1f%%"></span><span class="o" style="width:%.1f%%"></span></div>`,
			100*float64(t.Allowed)/float64(total),
			100*float64(t.RateLimited)/float64(total),
			100*float64(t.Other)/float64(total),
		)
		fmt.Fprintf(&b, `<p class="meta">allow %d · 429 %d · other %d · total %d</p>`, t.Allowed, t.RateLimited, t.Other, t.Total)

		if len(s.Analytics.ProxyInstances) > 0 {
			keys := make([]string, 0, len(s.Analytics.ProxyInstances))
			for k := range s.Analytics.ProxyInstances {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			parts := make([]string, 0, len(keys))
			for _, k := range keys {
				parts = append(parts, fmt.Sprintf("%s=%d", k, s.Analytics.ProxyInstances[k]))
			}
			fmt.Fprintf(&b, `<p class="meta">Proxy instances: %s · upstream forwards %d · mock Δ %d</p>`,
				html.EscapeString(strings.Join(parts, ", ")),
				s.Analytics.UpstreamForwards,
				s.Analytics.MockLogDelta.Total,
			)
		}

		if s.Quota != nil {
			fmt.Fprintf(&b, `<p class="meta">Quota: remaining=%d consumed=%d limit=%d (%s)</p>`,
				s.Quota.Remaining, s.Quota.Consumed, s.Quota.Limit, html.EscapeString(s.Quota.Algorithm))
		}

		if len(s.VegetaRuns) > 0 {
			b.WriteString(`<table><thead><tr><th>Profile</th><th>Status</th><th>Requests</th><th>Success</th><th>429</th><th>p95</th></tr></thead><tbody>`)
			for _, vr := range s.VegetaRuns {
				req, succ, deny, p95 := "—", "—", "0", "—"
				if vr.Metrics != nil {
					req = fmt.Sprintf("%d", vr.Metrics.Requests)
					succ = fmt.Sprintf("%.1f%%", vr.Metrics.SuccessRatio*100)
					if vr.Metrics.StatusCodes != nil {
						deny = fmt.Sprintf("%d", vr.Metrics.StatusCodes["429"])
					}
					if vr.Metrics.LatencyP95 != "" {
						p95 = vr.Metrics.LatencyP95
					}
				}
				fmt.Fprintf(&b, `<tr><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td></tr>`,
					html.EscapeString(vr.Profile), html.EscapeString(vr.Status),
					html.EscapeString(req), html.EscapeString(succ), html.EscapeString(deny), html.EscapeString(p95))
			}
			b.WriteString(`</tbody></table>`)
		}

		if len(s.Steps) > 0 {
			b.WriteString(`<ul class="steps">`)
			for _, st := range s.Steps {
				cls := "bad"
				if st.OK {
					cls = "ok"
				}
				detail := ""
				if st.Detail != "" {
					detail = " — " + html.EscapeString(st.Detail)
				}
				fmt.Fprintf(&b, `<li class="%s"><strong>%s</strong>%s</li>`, cls, html.EscapeString(st.Name), detail)
			}
			b.WriteString(`</ul>`)
		}
		b.WriteString(`</article>`)
	}

	raw, _ := json.MarshalIndent(rep, "", "  ")
	b.WriteString(`<h2>Raw JSON</h2><pre>`)
	b.WriteString(html.EscapeString(string(raw)))
	b.WriteString(`</pre></main></body></html>`)
	return b.Bytes()
}
