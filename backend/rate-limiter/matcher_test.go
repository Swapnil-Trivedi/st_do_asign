package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMatchHeaderPathMethod_All(t *testing.T) {
	snap := demoSnapshot(MatchModeAll, 10)
	req := httptest.NewRequest(http.MethodGet, "/1001/deviceList", nil)
	req.Header.Set("US-API-Key", "ak_demo_1001_alpha")

	got, ok := matchRequest(snap, req)
	require.True(t, ok)
	require.Equal(t, "demo", got.Namespace.Namespace.Label)
	require.Equal(t, "hdr:US-API-Key:ak_demo_1001_alpha", got.Subject)
}

func TestMatchRejectsMissingHeader(t *testing.T) {
	snap := demoSnapshot(MatchModeAll, 10)
	req := httptest.NewRequest(http.MethodGet, "/1001/deviceList", nil)

	_, ok := matchRequest(snap, req)
	require.False(t, ok)
}

func TestMatchModeAny(t *testing.T) {
	snap := demoSnapshot(MatchModeAny, 10)
	req := httptest.NewRequest(http.MethodPost, "/other", nil)
	req.Header.Set("US-API-Key", "ak_demo_1001_alpha")

	got, ok := matchRequest(snap, req)
	require.True(t, ok)
	require.Equal(t, "hdr:US-API-Key:ak_demo_1001_alpha", got.Subject)
}

func TestPriorityAndSpecificity(t *testing.T) {
	low := CompiledNamespace{
		Namespace: Namespace{ID: "a", Label: "low", Priority: 5, MatchMode: MatchModeAll, Algorithm: AlgoFixedToken},
		Rules: []Rule{{
			Match: RuleMatch{Kind: MatchMethod, Op: "eq", Value: "GET"},
		}},
		Config: AlgorithmConfig{Algorithm: AlgoFixedToken, Params: map[string]any{"sustainedRps": 1, "burst": 1}},
	}
	highSpec := CompiledNamespace{
		Namespace: Namespace{ID: "b", Label: "header-ns", Priority: 5, MatchMode: MatchModeAll, Algorithm: AlgoFixedToken},
		Rules: []Rule{{
			Match: RuleMatch{Kind: MatchHeader, Name: "US-API-Key", Op: "eq", Value: "k"},
		}},
		Config: AlgorithmConfig{Algorithm: AlgoFixedToken, Params: map[string]any{"sustainedRps": 1, "burst": 1}},
	}
	snap := &PolicySnapshot{Namespaces: []CompiledNamespace{low, highSpec}}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("US-API-Key", "k")

	got, ok := matchRequest(snap, req)
	require.True(t, ok)
	require.Equal(t, "header-ns", got.Namespace.Namespace.Label)
}

func TestPathAttrAndSubject(t *testing.T) {
	ns := CompiledNamespace{
		Namespace: Namespace{ID: "t", Label: "tenant", Priority: 1, MatchMode: MatchModeAll, Algorithm: AlgoFixedWindow},
		Rules: []Rule{{
			Match: RuleMatch{
				Kind: MatchPathAttr, Pattern: `^/(?P<tenantId>[0-9]+)/deviceList$`,
				Attr: "tenantId", Op: "eq", Value: "1001",
			},
		}},
		Config: AlgorithmConfig{Algorithm: AlgoFixedWindow, Params: map[string]any{"limit": 10, "windowSeconds": 1}},
	}
	snap := &PolicySnapshot{Namespaces: []CompiledNamespace{ns}}
	req := httptest.NewRequest(http.MethodGet, "/1001/deviceList", nil)

	got, ok := matchRequest(snap, req)
	require.True(t, ok)
	require.Equal(t, "tenant:1001", got.Subject)
}

func TestPathPrefixAndRegex(t *testing.T) {
	ns := CompiledNamespace{
		Namespace: Namespace{ID: "p", Label: "prefix", Priority: 1, MatchMode: MatchModeAll, Algorithm: AlgoFixedWindow},
		Rules: []Rule{
			{Match: RuleMatch{Kind: MatchPath, Op: "prefix", Value: "/1001/"}},
			{Match: RuleMatch{Kind: MatchRegex, Field: "path", Pattern: `^/1001/.*$`}},
		},
		Config: AlgorithmConfig{Algorithm: AlgoFixedWindow, Params: map[string]any{"limit": 1, "windowSeconds": 1}},
	}
	snap := &PolicySnapshot{Namespaces: []CompiledNamespace{ns}}
	req := httptest.NewRequest(http.MethodGet, "/1001/deviceList", nil)

	got, ok := matchRequest(snap, req)
	require.True(t, ok)
	require.Equal(t, "path:/1001/deviceList", got.Subject)
}

func TestHeaderInOp(t *testing.T) {
	ns := CompiledNamespace{
		Namespace: Namespace{ID: "h", Label: "in", Priority: 1, MatchMode: MatchModeAll, Algorithm: AlgoFixedToken},
		Rules: []Rule{{
			Match: RuleMatch{Kind: MatchHeader, Name: "US-API-Key", Op: "in", Values: []string{"a", "b"}},
		}},
		Config: AlgorithmConfig{Algorithm: AlgoFixedToken, Params: map[string]any{"sustainedRps": 1, "burst": 1}},
	}
	snap := &PolicySnapshot{Namespaces: []CompiledNamespace{ns}}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("US-API-Key", "b")

	got, ok := matchRequest(snap, req)
	require.True(t, ok)
	require.Equal(t, "hdr:US-API-Key:b", got.Subject)
}

