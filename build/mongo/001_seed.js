// Applied once on empty mongo volume (docker-entrypoint-initdb.d).
// DB name comes from MONGO_INITDB_DATABASE (rate_limiter).

const dbName = process.env.MONGO_INITDB_DATABASE || 'rate_limiter';
const db = db.getSiblingDB(dbName);

db.algorithms.deleteMany({});
db.algorithms.insertMany([
  {
    _id: 'sliding_window',
    label: 'Sliding window',
    description: 'Rolling window of request timestamps.',
    paramsSchema: {
      limit: { type: 'int', min: 1, required: true },
      windowSeconds: { type: 'int', min: 1, required: true },
    },
    enabled: true,
  },
  {
    _id: 'fixed_token',
    label: 'Fixed token (token bucket)',
    description: 'Refill at sustained RPS; burst capacity for spikes.',
    paramsSchema: {
      sustainedRps: { type: 'int', min: 1, required: true },
      burst: { type: 'int', min: 1, required: true },
    },
    enabled: true,
  },
  {
    _id: 'fixed_window',
    label: 'Fixed window',
    description: 'Hard count per discrete time window.',
    paramsSchema: {
      limit: { type: 'int', min: 1, required: true },
      windowSeconds: { type: 'int', min: 1, required: true },
    },
    enabled: true,
  },
]);

db.algorithm_configs.deleteMany({ name: 'demo-fixed-token-10-20' });
const configId = ObjectId();
db.algorithm_configs.insertOne({
  _id: configId,
  name: 'demo-fixed-token-10-20',
  algorithm: 'fixed_token',
  params: { sustainedRps: 10, burst: 20 },
  enabled: true,
  createdAt: new Date(),
  updatedAt: new Date(),
});

db.namespaces.deleteMany({ label: 'demo-deviceList' });
const nsId = ObjectId();
db.namespaces.insertOne({
  _id: nsId,
  label: 'demo-deviceList',
  upstreamHost: 'http://mock-server:8081',
  path: null,
  algorithm: 'fixed_token',
  configId: configId,
  matchMode: 'all',
  priority: 10,
  enabled: true,
  createdAt: new Date(),
  updatedAt: new Date(),
});

db.rules.deleteMany({ namespaceId: nsId });
db.rules.insertMany([
  {
    namespaceId: nsId,
    label: 'US-API-Key demo',
    enabled: true,
    match: {
      kind: 'header',
      name: 'US-API-Key',
      op: 'eq',
      value: 'ak_demo_1001_alpha',
    },
    createdAt: new Date(),
    updatedAt: new Date(),
  },
  {
    namespaceId: nsId,
    label: 'deviceList path',
    enabled: true,
    match: { kind: 'path', op: 'eq', value: '/1001/deviceList' },
    createdAt: new Date(),
    updatedAt: new Date(),
  },
  {
    namespaceId: nsId,
    label: 'GET only',
    enabled: true,
    match: { kind: 'method', op: 'eq', value: 'GET' },
    createdAt: new Date(),
    updatedAt: new Date(),
  },
]);

db.namespaces.createIndex({ label: 1 }, { unique: true });
db.namespaces.createIndex({ enabled: 1, priority: 1 });
db.rules.createIndex({ namespaceId: 1, enabled: 1 });
db.rules.createIndex({ 'match.kind': 1, 'match.name': 1 });
db.algorithm_configs.createIndex({ name: 1 }, { unique: true });
db.algorithm_configs.createIndex({ algorithm: 1, enabled: 1 });

print('seeded rate_limiter: algorithms, demo namespace, rules');
