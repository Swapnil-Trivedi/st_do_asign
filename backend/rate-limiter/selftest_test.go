package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestSelftestUnitSuite(t *testing.T) {
	report := (&selftestRunner{}).executeSuites(context.Background(), "unit")
	require.Equal(t, "unit", report.Suite)
	require.True(t, report.OK, "failures=%v", failedNames(report))
	require.Greater(t, report.Passed, 0)
}

func TestSelftestFunctionalWithMiniRedis(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(mr.Close)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	snap := demoSnapshot(MatchModeAll, 10)
	// Align label with seed so ft-match-loaded-policy asserts the happy path.
	snap.Namespaces[0].Namespace.Label = "demo-deviceList"
	cache := newPolicyCache(&staticPolicyLoader{snap: snap}, time.Hour)
	require.NoError(t, cache.LoadOnce(context.Background()))

	runner := &selftestRunner{cache: cache, limiter: newLimiter(rdb)}
	report := runner.executeSuites(context.Background(), "functional")
	require.True(t, report.OK, "failures=%v", failedNames(report))
}

func TestSelftestHTTPCatalogAndRun(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(mr.Close)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	cache := newPolicyCache(&staticPolicyLoader{snap: &PolicySnapshot{}}, time.Hour)
	require.NoError(t, cache.LoadOnce(context.Background()))
	runner := &selftestRunner{cache: cache, limiter: newLimiter(rdb)}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tests/catalog", nil)
	rec := httptest.NewRecorder()
	runner.listTestCatalog(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	req = httptest.NewRequest(http.MethodPost, "/api/v1/tests/run?suite=unit", nil)
	rec = httptest.NewRecorder()
	runner.runTests(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var report TestRunReport
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&report))
	require.True(t, report.OK)
	require.Equal(t, "unit", report.Suite)
}

type staticPolicyLoader struct {
	snap *PolicySnapshot
}

func (s *staticPolicyLoader) LoadSnapshot(context.Context) (*PolicySnapshot, error) {
	return s.snap, nil
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
