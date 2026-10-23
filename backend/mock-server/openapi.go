package main

import "net/http"

const openAPIJSON = `{
  "openapi": "3.0.3",
  "info": {
    "title": "Mock Upstream API",
    "version": "1.0.0",
    "description": "Data-plane mock backend. Auth header US-API-Key is logged/echoed, not validated. ~500ms business delay."
  },
  "servers": [{ "url": "http://localhost:8081" }],
  "paths": {
    "/healthz": {
      "get": {
        "summary": "Liveness",
        "responses": { "200": { "description": "OK" } }
      }
    },
    "/{tenantId}/deviceList": {
      "get": {
        "summary": "List devices for tenant",
        "parameters": [
          { "name": "tenantId", "in": "path", "required": true, "schema": { "type": "integer", "format": "int32" } },
          { "name": "US-API-Key", "in": "header", "required": false, "schema": { "type": "string" } }
        ],
        "responses": {
          "200": {
            "description": "Device list",
            "headers": {
              "US-API-Key": { "schema": { "type": "string" } },
              "US-Tenant": { "schema": { "type": "string" } },
              "US-Upstream-Latency-Ms": { "schema": { "type": "string" } },
              "US-Upstream-Response": { "schema": { "type": "string" } },
              "US-Mapped-API-Keys": { "schema": { "type": "string" } },
              "US-API-Key-Match": { "schema": { "type": "string" } }
            }
          }
        }
      }
    },
    "/{tenantId}/registerDevice": {
      "post": {
        "summary": "Register device",
        "parameters": [
          { "name": "tenantId", "in": "path", "required": true, "schema": { "type": "integer", "format": "int32" } },
          { "name": "US-API-Key", "in": "header", "required": false, "schema": { "type": "string" } }
        ],
        "requestBody": {
          "content": { "application/json": { "schema": { "type": "object" } } }
        },
        "responses": { "201": { "description": "Created" } }
      },
      "put": {
        "summary": "Update device registration",
        "parameters": [
          { "name": "tenantId", "in": "path", "required": true, "schema": { "type": "integer", "format": "int32" } },
          { "name": "US-API-Key", "in": "header", "required": false, "schema": { "type": "string" } }
        ],
        "requestBody": {
          "content": { "application/json": { "schema": { "type": "object" } } }
        },
        "responses": { "200": { "description": "Updated" } }
      }
    },
    "/api/v1/logs": {
      "get": {
        "summary": "Recent access logs (ring buffer)",
        "parameters": [
          { "name": "limit", "in": "query", "schema": { "type": "integer", "default": 100 } }
        ],
        "responses": { "200": { "description": "Log entries" } }
      }
    }
  }
}`

func serveOpenAPI(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(openAPIJSON))
}

func serveDocs(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(`<!DOCTYPE html>
<html>
<head>
  <title>Mock Upstream — Swagger</title>
  <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css" />
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
  <script>
    window.ui = SwaggerUIBundle({
      url: '/openapi.json',
      dom_id: '#swagger-ui',
      presets: [SwaggerUIBundle.presets.apis],
    });
  </script>
</body>
</html>`))
}
