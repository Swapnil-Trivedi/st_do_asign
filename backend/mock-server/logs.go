package main

import (
	"sync"
	"time"
)

type logEntry struct {
	TS      time.Time `json:"ts"`
	Level   string    `json:"level"`
	Message string    `json:"message"`
}

type logRing struct {
	mu   sync.RWMutex
	buf  []logEntry
	cap  int
	seq  uint64
}

func newLogRing(capacity int) *logRing {
	if capacity < 32 {
		capacity = 32
	}
	return &logRing{buf: make([]logEntry, 0, capacity), cap: capacity}
}

func (l *logRing) add(level, msg string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.seq++
	e := logEntry{TS: time.Now().UTC(), Level: level, Message: msg}
	if len(l.buf) < l.cap {
		l.buf = append(l.buf, e)
		return
	}
	// drop oldest
	copy(l.buf, l.buf[1:])
	l.buf[len(l.buf)-1] = e
}

func (l *logRing) snapshot(limit int) []logEntry {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if limit <= 0 || limit > len(l.buf) {
		limit = len(l.buf)
	}
	start := len(l.buf) - limit
	out := make([]logEntry, limit)
	copy(out, l.buf[start:])
	return out
}

var accessLogs = newLogRing(500)
