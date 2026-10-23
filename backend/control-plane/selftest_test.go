package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
)

func TestSelftestUnitSuiteViaHTTP(t *testing.T) {
	a := &api{store: newMemoryStore()}
	r := chi.NewRouter()
	a.routes(r)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/tests/run?suite=unit", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var report TestRunReport
	require.NoError(t, json.NewDecoder(w.Body).Decode(&report))
	require.Equal(t, "unit", report.Suite)
	require.Greater(t, report.Total, 0)
	require.True(t, report.OK, "failures: %+v", failedNames(report))
	require.NotEmpty(t, report.Results[0].Description)
}

func TestSelftestFunctionalAndConcurrencyInMemory(t *testing.T) {
	a := &api{store: newMemoryStore()}
	for _, suite := range []string{"functional", "concurrency"} {
		report := a.executeSuites(context.Background(), suite)
		require.True(t, report.OK, "suite=%s failures=%v", suite, failedNames(report))
		require.Greater(t, report.Passed, 0)
		for _, r := range report.Results {
			require.NotEmpty(t, r.Description)
		}
	}
}

func TestSelftestE2ESuiteSkipsWithoutProxy(t *testing.T) {
	t.Setenv("RATE_LIMITER_URL", "http://127.0.0.1:1")
	a := &api{store: newMemoryStore()}
	report := a.executeSuites(context.Background(), "e2e")
	require.True(t, report.OK, failedNames(report))
	require.Greater(t, report.Skipped, 0)
	require.Equal(t, 0, report.Failed)
	require.GreaterOrEqual(t, report.Total, 4)
	for _, r := range report.Results {
		require.NotEmpty(t, r.Description)
		require.Contains(t, []string{"passed", "skipped"}, r.Status)
	}
}

func TestSelftestAllIncludesE2E(t *testing.T) {
	t.Setenv("RATE_LIMITER_URL", "http://127.0.0.1:1")
	a := &api{store: newMemoryStore()}
	report := a.executeSuites(context.Background(), "all")
	require.True(t, report.OK, failedNames(report))
	var sawConc, sawE2E bool
	for _, r := range report.Results {
		if r.Suite == "concurrency" {
			sawConc = true
		}
		if r.Suite == "e2e" {
			sawE2E = true
		}
	}
	require.True(t, sawConc)
	require.True(t, sawE2E)
}

func failedNames(report TestRunReport) []string {
	var out []string
	for _, r := range report.Results {
		if r.Status == "failed" {
			out = append(out, r.ID+": "+r.Message)
		}
	}
	return out
}
