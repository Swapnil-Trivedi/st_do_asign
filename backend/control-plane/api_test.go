package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
)

func testServer(t *testing.T) http.Handler {
	t.Helper()
	a := &api{store: newMemoryStore()}
	r := chi.NewRouter()
	a.routes(r)
	return r
}

func decodeJSON(t *testing.T, res *http.Response, dest any) {
	t.Helper()
	defer res.Body.Close()
	require.NoError(t, json.NewDecoder(res.Body).Decode(dest))
}

func TestHealthz(t *testing.T) {
	srv := testServer(t)
	res := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, res)
	require.Equal(t, http.StatusOK, w.Code)
}

func TestAlgorithmsCatalog(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/algorithms", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	var body struct {
		Algorithms []Algorithm `json:"algorithms"`
	}
	decodeJSON(t, w.Result(), &body)
	require.Len(t, body.Algorithms, 3)
}

func TestConfigCRUDAndValidation(t *testing.T) {
	srv := testServer(t)

	// invalid params
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/configs", bytes.NewBufferString(
		`{"name":"bad","algorithm":"fixed_token","params":{"sustainedRps":0,"burst":5}}`,
	)))
	require.Equal(t, http.StatusBadRequest, w.Code)

	// create
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/configs", bytes.NewBufferString(
		`{"name":"cfg-a","algorithm":"fixed_token","params":{"sustainedRps":10,"burst":20}}`,
	)))
	require.Equal(t, http.StatusCreated, w.Code)
	var cfg AlgorithmConfig
	decodeJSON(t, w.Result(), &cfg)
	require.Equal(t, "cfg-a", cfg.Name)
	require.NotEmpty(t, cfg.ID)

	// duplicate name
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/configs", bytes.NewBufferString(
		`{"name":"cfg-a","algorithm":"fixed_token","params":{"sustainedRps":1,"burst":1}}`,
	)))
	require.Equal(t, http.StatusConflict, w.Code)

	// get + update
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/configs/"+cfg.ID, nil))
	require.Equal(t, http.StatusOK, w.Code)

	w = httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/api/v1/configs/"+cfg.ID, bytes.NewBufferString(
		`{"params":{"sustainedRps":15,"burst":30}}`,
	)))
	require.Equal(t, http.StatusOK, w.Code)
	var updated AlgorithmConfig
	decodeJSON(t, w.Result(), &updated)
	require.Equal(t, float64(15), updated.Params["sustainedRps"])

	// delete
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/api/v1/configs/"+cfg.ID, nil))
	require.Equal(t, http.StatusNoContent, w.Code)
}

func TestNamespaceAndRulesE2E(t *testing.T) {
	srv := testServer(t)

	// config first
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/configs", bytes.NewBufferString(
		`{"name":"demo-cfg","algorithm":"fixed_token","params":{"sustainedRps":10,"burst":20}}`,
	)))
	require.Equal(t, http.StatusCreated, w.Code)
	var cfg AlgorithmConfig
	decodeJSON(t, w.Result(), &cfg)

	// bad upstream
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/namespaces", bytes.NewBufferString(
		`{"label":"ns1","upstreamHost":"not-a-url","configId":"`+cfg.ID+`","matchMode":"all"}`,
	)))
	require.Equal(t, http.StatusBadRequest, w.Code)

	// create namespace
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/namespaces", bytes.NewBufferString(
		`{"label":"demo-deviceList","upstreamHost":"http://mock-server:8081","configId":"`+cfg.ID+`","matchMode":"all","priority":10}`,
	)))
	require.Equal(t, http.StatusCreated, w.Code)
	var ns Namespace
	decodeJSON(t, w.Result(), &ns)
	require.Equal(t, AlgoFixedToken, ns.Algorithm)
	require.Equal(t, cfg.ID, ns.ConfigID)

	// cannot delete in-use config
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/api/v1/configs/"+cfg.ID, nil))
	require.Equal(t, http.StatusConflict, w.Code)

	// add header rule
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/namespaces/"+ns.ID+"/rules", bytes.NewBufferString(
		`{"label":"api-key","match":{"kind":"header","name":"US-API-Key","op":"eq","value":"ak_demo_1001_alpha"}}`,
	)))
	require.Equal(t, http.StatusCreated, w.Code)
	var rule Rule
	decodeJSON(t, w.Result(), &rule)
	require.Equal(t, MatchHeader, rule.Match.Kind)

	// invalid rule
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/namespaces/"+ns.ID+"/rules", bytes.NewBufferString(
		`{"match":{"kind":"header","name":"","op":"eq","value":"x"}}`,
	)))
	require.Equal(t, http.StatusBadRequest, w.Code)

	// path + method rules
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/namespaces/"+ns.ID+"/rules", bytes.NewBufferString(
		`{"match":{"kind":"path","op":"eq","value":"/1001/deviceList"}}`,
	)))
	require.Equal(t, http.StatusCreated, w.Code)

	w = httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/namespaces/"+ns.ID+"/rules", bytes.NewBufferString(
		`{"match":{"kind":"method","op":"eq","value":"GET"}}`,
	)))
	require.Equal(t, http.StatusCreated, w.Code)

	// list rules
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/namespaces/"+ns.ID+"/rules", nil))
	require.Equal(t, http.StatusOK, w.Code)
	var rulesBody struct {
		Rules []Rule `json:"rules"`
	}
	decodeJSON(t, w.Result(), &rulesBody)
	require.Len(t, rulesBody.Rules, 3)

	// delete rule
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/api/v1/rules/"+rule.ID, nil))
	require.Equal(t, http.StatusNoContent, w.Code)

	// delete namespace cascades remaining rules
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/api/v1/namespaces/"+ns.ID, nil))
	require.Equal(t, http.StatusNoContent, w.Code)

	// config deletable now
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/api/v1/configs/"+cfg.ID, nil))
	require.Equal(t, http.StatusNoContent, w.Code)
}

func TestValidateHelpers(t *testing.T) {
	require.Error(t, validateConfigInput("nope", map[string]any{}))
	require.NoError(t, validateConfigInput(AlgoSlidingWindow, map[string]any{"limit": 5, "windowSeconds": 1}))
	require.Error(t, validateNamespaceInput("", "http://x", MatchModeAll, "id"))
	require.NoError(t, validateRuleMatch(RuleMatch{Kind: MatchRegex, Field: "path", Pattern: "^/x$"}))
}
