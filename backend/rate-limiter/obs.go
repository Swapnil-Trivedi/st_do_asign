package main

import (
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// TrafficEvent is one rate-limit decision observed on this proxy instance.
type TrafficEvent struct {
	ID         uint64    `json:"id"`
	TS         time.Time `json:"ts"`
	Instance   string    `json:"instance"`
	Method     string    `json:"method"`
	Path       string    `json:"path"`
	Decision   string    `json:"decision"` // allow | deny | no_match | open | error
	Namespace  string    `json:"namespace,omitempty"`
	Algorithm  string    `json:"algorithm,omitempty"`
	Subject    string    `json:"subject,omitempty"`
	Status     int       `json:"status"`
	Limit      int64     `json:"limit,omitempty"`
	Remaining  int64     `json:"remaining,omitempty"`
	ResetMs    int64     `json:"resetMs,omitempty"`
	LatencyMs  int64     `json:"latencyMs"`
	RequestID  string    `json:"requestId,omitempty"`
}

type nsCounters struct {
	Allowed uint64 `json:"allowed"`
	Denied  uint64 `json:"denied"`
}

type trafficObs struct {
	mu      sync.RWMutex
	buf     []TrafficEvent
	cap     int
	seq     uint64
	allowed atomic.Uint64
	denied  atomic.Uint64
	noMatch atomic.Uint64
	open    atomic.Uint64
	errors  atomic.Uint64
	byNS    map[string]*nsCounters
}

func newTrafficObs(capacity int) *trafficObs {
	if capacity < 64 {
		capacity = 64
	}
	return &trafficObs{
		buf:  make([]TrafficEvent, 0, capacity),
		cap:  capacity,
		byNS: map[string]*nsCounters{},
	}
}

func (o *trafficObs) record(e TrafficEvent) {
	e.TS = time.Now().UTC()
	e.Instance = envOr("INSTANCE_ID", "1")

	o.mu.Lock()
	o.seq++
	e.ID = o.seq
	if len(o.buf) < o.cap {
		o.buf = append(o.buf, e)
	} else {
		copy(o.buf, o.buf[1:])
		o.buf[len(o.buf)-1] = e
	}
	if e.Namespace != "" {
		c := o.byNS[e.Namespace]
		if c == nil {
			c = &nsCounters{}
			o.byNS[e.Namespace] = c
		}
		switch e.Decision {
		case "allow":
			c.Allowed++
		case "deny":
			c.Denied++
		}
	}
	o.mu.Unlock()

	switch e.Decision {
	case "allow":
		o.allowed.Add(1)
	case "deny":
		o.denied.Add(1)
	case "no_match":
		o.noMatch.Add(1)
	case "open":
		o.open.Add(1)
	default:
		o.errors.Add(1)
	}
}

func (o *trafficObs) snapshot(limit int) ([]TrafficEvent, map[string]nsCounters) {
	o.mu.RLock()
	defer o.mu.RUnlock()
	if limit <= 0 || limit > len(o.buf) {
		limit = len(o.buf)
	}
	start := len(o.buf) - limit
	out := make([]TrafficEvent, limit)
	copy(out, o.buf[start:])
	by := make(map[string]nsCounters, len(o.byNS))
	for k, v := range o.byNS {
		by[k] = *v
	}
	return out, by
}

func (o *trafficObs) reset() {
	o.mu.Lock()
	o.buf = o.buf[:0]
	o.byNS = map[string]*nsCounters{}
	o.mu.Unlock()
	o.allowed.Store(0)
	o.denied.Store(0)
	o.noMatch.Store(0)
	o.open.Store(0)
	o.errors.Store(0)
}

func (h *proxyHandler) serveTraffic(w http.ResponseWriter, r *http.Request) {
	limit := 120
	if q := r.URL.Query().Get("limit"); q != "" {
		if n, err := strconv.Atoi(q); err == nil && n > 0 {
			limit = n
			if limit > 500 {
				limit = 500
			}
		}
	}
	events, byNS := h.obs.snapshot(limit)
	snap := h.cache.Snapshot()
	nsCount := 0
	if snap != nil {
		nsCount = len(snap.Namespaces)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"instance": envOr("INSTANCE_ID", "1"),
		"observedAt": time.Now().UTC(),
		"policy": map[string]any{
			"loaded":     snap != nil,
			"namespaces": nsCount,
		},
		"counters": map[string]uint64{
			"allowed":  h.obs.allowed.Load(),
			"denied":   h.obs.denied.Load(),
			"noMatch":  h.obs.noMatch.Load(),
			"open":     h.obs.open.Load(),
			"errors":   h.obs.errors.Load(),
			"total":    h.obs.allowed.Load() + h.obs.denied.Load() + h.obs.noMatch.Load() + h.obs.open.Load() + h.obs.errors.Load(),
		},
		"byNamespace": byNS,
		"events":      events,
	})
}

func (h *proxyHandler) resetTraffic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	h.obs.reset()
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type statusCapture struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (s *statusCapture) WriteHeader(code int) {
	if !s.wrote {
		s.status = code
		s.wrote = true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusCapture) Write(b []byte) (int, error) {
	if !s.wrote {
		s.status = http.StatusOK
		s.wrote = true
	}
	return s.ResponseWriter.Write(b)
}

func (s *statusCapture) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
