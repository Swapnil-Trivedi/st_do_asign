# React console (`st_do_asign/ui`)

Operator UI for the distributed rate limiter: Overview, Rate Limiter (control plane), Mock Backend, Vegeta Client (data plane).

```bash
# stack first
../build/build.sh up

npm install
npm run dev
# → http://localhost:5173
```

| Env | Default |
|-----|---------|
| `VITE_MOCK_URL` | `http://localhost:8081` |
| `VITE_VEGETA_URL` | `http://localhost:8090` |
| `VITE_PROXY_URL` | `http://localhost:8080` |
| `VITE_CONTROL_PLANE_URL` | `http://localhost:8091` |
| `VITE_CONSOLE_URL` | `http://localhost:8095` |

Project docs (Mongo / Redis / sequences): see repo root [`README.md`](../README.md).
