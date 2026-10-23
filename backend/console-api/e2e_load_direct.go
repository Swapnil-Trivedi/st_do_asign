package main

import (
	"net/http"
	"strconv"
	"sync"
)

type directHit struct {
	Status  int
	Headers http.Header
}

func (c *httpClient) runDirect(path, apiKey string, count int, concurrent bool) (TrafficStats, ServiceAnalytics) {
	traffic := TrafficStats{StatusCodes: map[string]int{}}
	analytics := ServiceAnalytics{ProxyInstances: map[string]int{}}

	hits := make([]directHit, count)
	if concurrent {
		var wg sync.WaitGroup
		wg.Add(count)
		for i := 0; i < count; i++ {
			i := i
			go func() {
				defer wg.Done()
				hits[i] = c.oneHit(path, apiKey)
			}()
		}
		wg.Wait()
	} else {
		for i := 0; i < count; i++ {
			hits[i] = c.oneHit(path, apiKey)
		}
	}

	for _, h := range hits {
		traffic.Total++
		code := strconv.Itoa(h.Status)
		traffic.StatusCodes[code]++
		switch {
		case h.Status >= 200 && h.Status < 300:
			traffic.Allowed++
			if analytics.RateLimitHeaders.LastAllow == nil {
				analytics.RateLimitHeaders.LastAllow = headerMap(h.Headers,
					"X-RateLimit-Limit", "X-RateLimit-Remaining", "X-RateLimit-Reset", "X-Proxy-Instance",
					"US-Upstream-Response", "US-API-Key-Match")
			}
			if inst := h.Headers.Get("X-Proxy-Instance"); inst != "" {
				analytics.ProxyInstances[inst]++
			}
			if h.Headers.Get("US-Upstream-Response") == "mock-server" {
				analytics.UpstreamForwards++
			}
			if h.Headers.Get("US-API-Key-Match") == "true" {
				analytics.KeyMatchTrue++
			}
			if ms, ok := parseLatencyMs(h.Headers); ok && len(analytics.UpstreamLatencyMs) < 32 {
				analytics.UpstreamLatencyMs = append(analytics.UpstreamLatencyMs, ms)
			}
		case h.Status == 429:
			traffic.RateLimited++
			if analytics.RateLimitHeaders.LastDeny == nil {
				analytics.RateLimitHeaders.LastDeny = headerMap(h.Headers,
					"X-RateLimit-Limit", "X-RateLimit-Remaining", "X-RateLimit-Reset", "Retry-After", "X-Proxy-Instance")
			}
			if inst := h.Headers.Get("X-Proxy-Instance"); inst != "" {
				analytics.ProxyInstances[inst]++
			}
		default:
			traffic.Other++
			if inst := h.Headers.Get("X-Proxy-Instance"); inst != "" {
				analytics.ProxyInstances[inst]++
			}
		}
	}
	return traffic, analytics
}

func (c *httpClient) oneHit(path, apiKey string) directHit {
	url := c.u.RateLimiter + path
	code, _, hdr, err := c.doJSON(http.MethodGet, url, map[string]string{"US-API-Key": apiKey}, nil)
	if err != nil {
		return directHit{Status: 0}
	}
	return directHit{Status: code, Headers: hdr}
}

func mergeTraffic(a, b TrafficStats) TrafficStats {
	out := TrafficStats{
		Total: a.Total + b.Total, Allowed: a.Allowed + b.Allowed,
		RateLimited: a.RateLimited + b.RateLimited, Other: a.Other + b.Other,
		StatusCodes: map[string]int{},
	}
	for k, v := range a.StatusCodes {
		out.StatusCodes[k] += v
	}
	for k, v := range b.StatusCodes {
		out.StatusCodes[k] += v
	}
	return out
}

func mergeAnalytics(a, b ServiceAnalytics) ServiceAnalytics {
	out := a
	if out.ProxyInstances == nil {
		out.ProxyInstances = map[string]int{}
	}
	for k, v := range b.ProxyInstances {
		out.ProxyInstances[k] += v
	}
	out.UpstreamForwards += b.UpstreamForwards
	out.KeyMatchTrue += b.KeyMatchTrue
	out.UpstreamLatencyMs = append(out.UpstreamLatencyMs, b.UpstreamLatencyMs...)
	if len(out.UpstreamLatencyMs) > 32 {
		out.UpstreamLatencyMs = out.UpstreamLatencyMs[:32]
	}
	if out.RateLimitHeaders.LastAllow == nil {
		out.RateLimitHeaders.LastAllow = b.RateLimitHeaders.LastAllow
	}
	if out.RateLimitHeaders.LastDeny == nil {
		out.RateLimitHeaders.LastDeny = b.RateLimitHeaders.LastDeny
	}
	out.ConcurrentVegetaJobs += b.ConcurrentVegetaJobs
	return out
}

func mockLogDelta(before, after []map[string]any) MockLogStats {
	// Logs are newest-first ring; count by level in after that aren't in before by comparing lengths crudely.
	stats := MockLogStats{}
	beforeN := len(before)
	afterN := len(after)
	delta := afterN - beforeN
	if delta < 0 {
		delta = len(after) // wrapped; count all current
	}
	if delta > len(after) {
		delta = len(after)
	}
	for i := 0; i < delta && i < len(after); i++ {
		level, _ := after[i]["level"].(string)
		stats.Total++
		switch level {
		case "access":
			stats.Access++
		case "upstream":
			stats.Upstream++
		case "warn":
			stats.Warn++
		default:
			stats.Info++
		}
	}
	return stats
}
