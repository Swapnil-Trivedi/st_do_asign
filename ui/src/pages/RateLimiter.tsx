import { useCallback, useEffect, useMemo, useState } from 'react'
import { ControlPlaneDocsPanel } from '../components/ControlPlaneDocsPanel'
import { PlaneTag } from '../components/PlaneTag'
import { RateLimiterObsDeck } from '../components/RateLimiterObsDeck'
import {
  ControlPlaneError,
  createConfig,
  createNamespace,
  createRule,
  defaultParamsForAlgorithm,
  deleteConfig,
  deleteNamespace,
  deleteRule,
  fetchAlgorithms,
  fetchConfigs,
  fetchNamespaces,
  fetchRules,
  formatMatch,
  type Algorithm,
  type AlgorithmConfig,
  type Namespace,
  type Rule,
  type RuleMatch,
} from '../lib/controlPlaneApi'
import { SERVICES } from '../lib/config'

type Section = 'obs' | 'configs' | 'namespaces' | 'docs'

function errMsg(e: unknown): string {
  if (e instanceof ControlPlaneError) return e.message
  if (e instanceof Error) return e.message
  return 'unexpected error'
}

export function RateLimiter() {
  const [section, setSection] = useState<Section>('obs')
  const [algorithms, setAlgorithms] = useState<Algorithm[]>([])
  const [configs, setConfigs] = useState<AlgorithmConfig[]>([])
  const [namespaces, setNamespaces] = useState<Namespace[]>([])
  const [selectedNs, setSelectedNs] = useState<string | null>(null)
  const [rules, setRules] = useState<Rule[]>([])
  const [error, setError] = useState<string | null>(null)
  const [ok, setOk] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  // config form
  const [cfgName, setCfgName] = useState('')
  const [cfgAlgo, setCfgAlgo] = useState('fixed_token')
  const [cfgParams, setCfgParams] = useState<Record<string, number>>(
    defaultParamsForAlgorithm('fixed_token'),
  )

  // namespace form
  const [nsLabel, setNsLabel] = useState('')
  const [nsUpstream, setNsUpstream] = useState(SERVICES.mockServer.networkUrl)
  const [nsPath, setNsPath] = useState('')
  const [nsConfigId, setNsConfigId] = useState('')
  const [nsMatchMode, setNsMatchMode] = useState<'all' | 'any'>('all')
  const [nsPriority, setNsPriority] = useState(100)

  // rule form
  const [ruleKind, setRuleKind] = useState<RuleMatch['kind']>('header')
  const [ruleName, setRuleName] = useState('US-API-Key')
  const [ruleOp, setRuleOp] = useState('eq')
  const [ruleValue, setRuleValue] = useState('ak_demo_1001_alpha')
  const [rulePattern, setRulePattern] = useState('')
  const [ruleAttr, setRuleAttr] = useState('tenantId')
  const [ruleField, setRuleField] = useState('path')

  const refresh = useCallback(async () => {
    setError(null)
    try {
      const [a, c, n] = await Promise.all([
        fetchAlgorithms(),
        fetchConfigs(),
        fetchNamespaces(),
      ])
      setAlgorithms(a.algorithms ?? [])
      setConfigs(c.configs ?? [])
      setNamespaces(n.namespaces ?? [])
      setNsConfigId((prev) => prev || c.configs?.[0]?.id || '')
    } catch (e) {
      setError(errMsg(e))
    }
  }, [])

  const refreshRules = useCallback(async (nsId: string) => {
    try {
      const data = await fetchRules(nsId)
      setRules(data.rules ?? [])
    } catch (e) {
      setError(errMsg(e))
    }
  }, [])

  useEffect(() => {
    void refresh()
  }, [refresh])

  // Keep namespace list fresh while watching the obs deck (gauges bind to labels).
  useEffect(() => {
    if (section !== 'obs') return
    const id = window.setInterval(() => void refresh(), 4000)
    return () => window.clearInterval(id)
  }, [section, refresh])

  useEffect(() => {
    if (!selectedNs) {
      setRules([])
      return
    }
    void refreshRules(selectedNs)
  }, [selectedNs, refreshRules])

  const algoOptions = useMemo(
    () => algorithms.filter((a) => a.enabled),
    [algorithms],
  )

  async function onCreateConfig() {
    setBusy(true)
    setError(null)
    setOk(null)
    try {
      const created = await createConfig({
        name: cfgName.trim(),
        algorithm: cfgAlgo,
        params: cfgParams,
      })
      setOk(`Created config ${created.name}`)
      setCfgName('')
      await refresh()
      setNsConfigId(created.id)
    } catch (e) {
      setError(errMsg(e))
    } finally {
      setBusy(false)
    }
  }

  async function onDeleteConfig(id: string) {
    if (!window.confirm('Delete this algorithm config?')) return
    setBusy(true)
    setError(null)
    setOk(null)
    try {
      await deleteConfig(id)
      setOk('Config deleted')
      await refresh()
    } catch (e) {
      setError(errMsg(e))
    } finally {
      setBusy(false)
    }
  }

  async function onCreateNamespace() {
    setBusy(true)
    setError(null)
    setOk(null)
    try {
      const created = await createNamespace({
        label: nsLabel.trim(),
        upstreamHost: nsUpstream.trim(),
        path: nsPath.trim() ? nsPath.trim() : null,
        configId: nsConfigId,
        matchMode: nsMatchMode,
        priority: nsPriority,
      })
      setOk(`Created namespace ${created.label}`)
      setNsLabel('')
      await refresh()
      setSelectedNs(created.id)
      setSection('namespaces')
    } catch (e) {
      setError(errMsg(e))
    } finally {
      setBusy(false)
    }
  }

  async function onDeleteNamespace(id: string) {
    if (!window.confirm('Delete namespace and its rules?')) return
    setBusy(true)
    setError(null)
    setOk(null)
    try {
      await deleteNamespace(id)
      if (selectedNs === id) setSelectedNs(null)
      setOk('Namespace deleted')
      await refresh()
    } catch (e) {
      setError(errMsg(e))
    } finally {
      setBusy(false)
    }
  }

  function buildMatch(): RuleMatch {
    if (ruleKind === 'header') {
      return { kind: 'header', name: ruleName, op: ruleOp, value: ruleValue }
    }
    if (ruleKind === 'path') {
      return { kind: 'path', op: ruleOp === 'prefix' ? 'prefix' : 'eq', value: ruleValue }
    }
    if (ruleKind === 'method') {
      return { kind: 'method', op: ruleOp, value: ruleValue.toUpperCase() }
    }
    if (ruleKind === 'path_attr') {
      return {
        kind: 'path_attr',
        pattern: rulePattern,
        attr: ruleAttr,
        op: 'eq',
        value: ruleValue,
      }
    }
    return { kind: 'regex', field: ruleField, pattern: rulePattern }
  }

  async function onCreateRule() {
    if (!selectedNs) return
    setBusy(true)
    setError(null)
    setOk(null)
    try {
      await createRule(selectedNs, { match: buildMatch() })
      setOk('Rule added')
      await refreshRules(selectedNs)
    } catch (e) {
      setError(errMsg(e))
    } finally {
      setBusy(false)
    }
  }

  async function onDeleteRule(id: string) {
    if (!selectedNs) return
    setBusy(true)
    setError(null)
    setOk(null)
    try {
      await deleteRule(id)
      setOk('Rule deleted')
      await refreshRules(selectedNs)
    } catch (e) {
      setError(errMsg(e))
    } finally {
      setBusy(false)
    }
  }

  const selected = namespaces.find((n) => n.id === selectedNs) ?? null

  return (
    <div className="page">
      <header className="page__header">
        <div>
          <div className="page__eyebrow">
            <PlaneTag plane="control" />
          </div>
          <h1>Rate Limiter</h1>
          <p className="muted">
            Live obs of quota enforcement + control-plane policies (Mongo). CP API:{' '}
            <code>{SERVICES.controlPlane.hostUrl}</code> · proxy{' '}
            <code>{SERVICES.rateLimiter.hostUrl}</code>
          </p>
        </div>
        {section !== 'obs' && section !== 'docs' ? (
          <button type="button" className="btn btn--ghost" disabled={busy} onClick={() => void refresh()}>
            Refresh
          </button>
        ) : null}
      </header>

      <div className="tabs" role="tablist" aria-label="Rate limiter sections">
        <button
          type="button"
          role="tab"
          aria-selected={section === 'obs'}
          className={`tabs__btn${section === 'obs' ? ' tabs__btn--active' : ''}`}
          onClick={() => setSection('obs')}
        >
          Live obs
        </button>
        <button
          type="button"
          role="tab"
          aria-selected={section === 'namespaces'}
          className={`tabs__btn${section === 'namespaces' ? ' tabs__btn--active' : ''}`}
          onClick={() => setSection('namespaces')}
        >
          Namespaces &amp; rules
        </button>
        <button
          type="button"
          role="tab"
          aria-selected={section === 'configs'}
          className={`tabs__btn${section === 'configs' ? ' tabs__btn--active' : ''}`}
          onClick={() => setSection('configs')}
        >
          Algorithm configs
        </button>
        <button
          type="button"
          role="tab"
          aria-selected={section === 'docs'}
          className={`tabs__btn${section === 'docs' ? ' tabs__btn--active' : ''}`}
          onClick={() => setSection('docs')}
        >
          API Docs
        </button>
      </div>

      {section !== 'docs' && section !== 'obs' && error ? (
        <p className="error" role="alert">
          {error}
        </p>
      ) : null}
      {section !== 'docs' && section !== 'obs' && ok ? <p className="ok-banner">{ok}</p> : null}

      {/* Keep obs deck mounted so the 1s poll survives tab switches within this page. */}
      <div hidden={section !== 'obs'} className={section === 'obs' ? undefined : 'page-slot--hidden'}>
        <RateLimiterObsDeck namespaces={namespaces} active={section === 'obs'} />
      </div>

      {section === 'docs' ? (
        <ControlPlaneDocsPanel />
      ) : section === 'configs' ? (
            <div className="split">
              <section className="panel">
                <h2>Create config</h2>
                <p className="muted">
                  Reusable knobs referenced by namespaces via <code>configId</code>.
                </p>
                <label className="field">
                  <span>Name</span>
                  <input
                    value={cfgName}
                    onChange={(e) => setCfgName(e.target.value)}
                    placeholder="demo-fixed-token-10-20"
                  />
                </label>
                <label className="field">
                  <span>Algorithm</span>
                  <select
                    value={cfgAlgo}
                    onChange={(e) => {
                      const v = e.target.value
                      setCfgAlgo(v)
                      setCfgParams(defaultParamsForAlgorithm(v))
                    }}
                  >
                    {algoOptions.map((a) => (
                      <option key={a.id} value={a.id}>
                        {a.label}
                      </option>
                    ))}
                  </select>
                </label>
                <div className="field-row">
                  {Object.keys(cfgParams).map((key) => (
                    <label key={key} className="field">
                      <span>{key}</span>
                      <input
                        type="number"
                        value={cfgParams[key]}
                        onChange={(e) =>
                          setCfgParams((p) => ({ ...p, [key]: Number(e.target.value) }))
                        }
                      />
                    </label>
                  ))}
                </div>
                <div className="btn-row">
                  <button
                    type="button"
                    className="btn"
                    disabled={busy || !cfgName.trim()}
                    onClick={() => void onCreateConfig()}
                  >
                    Create config
                  </button>
                </div>
              </section>

              <section className="panel">
                <h2>Existing configs</h2>
                {configs.length === 0 ? (
                  <p className="muted">No configs yet.</p>
                ) : (
                  <div className="table-wrap">
                    <table className="data-table">
                      <thead>
                        <tr>
                          <th>Name</th>
                          <th>Algorithm</th>
                          <th>Params</th>
                          <th>Enabled</th>
                          <th />
                        </tr>
                      </thead>
                      <tbody>
                        {configs.map((c) => (
                          <tr key={c.id}>
                            <td>
                              <strong>{c.name}</strong>
                              <div className="muted data-table__sub">
                                <code>{c.id.slice(0, 8)}</code>
                              </div>
                            </td>
                            <td>
                              <code>{c.algorithm}</code>
                            </td>
                            <td>
                              <code>{JSON.stringify(c.params)}</code>
                            </td>
                            <td>{c.enabled ? 'yes' : 'no'}</td>
                            <td className="data-table__actions">
                              <button
                                type="button"
                                className="btn btn--ghost btn--tiny"
                                disabled={busy}
                                onClick={() => void onDeleteConfig(c.id)}
                              >
                                Delete
                              </button>
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                )}
              </section>
            </div>
          ) : section === 'namespaces' ? (
            <>
              <div className="split">
                <section className="panel">
                  <h2>Create namespace</h2>
                  <p className="muted">
                    Upstream + config binding. Rules below decide when the namespace applies.
                  </p>
                  <label className="field">
                    <span>Label</span>
                    <input
                      value={nsLabel}
                      onChange={(e) => setNsLabel(e.target.value)}
                      placeholder="demo-deviceList"
                    />
                  </label>
                  <label className="field">
                    <span>Upstream host</span>
                    <input value={nsUpstream} onChange={(e) => setNsUpstream(e.target.value)} />
                  </label>
                  <label className="field">
                    <span>Path rewrite (optional)</span>
                    <input
                      value={nsPath}
                      onChange={(e) => setNsPath(e.target.value)}
                      placeholder="leave empty to forward original path"
                    />
                  </label>
                  <label className="field">
                    <span>Algorithm config</span>
                    <select value={nsConfigId} onChange={(e) => setNsConfigId(e.target.value)}>
                      <option value="">Select config…</option>
                      {configs.map((c) => (
                        <option key={c.id} value={c.id}>
                          {c.name} ({c.algorithm})
                        </option>
                      ))}
                    </select>
                  </label>
                  <div className="field-row">
                    <label className="field">
                      <span>Match mode</span>
                      <select
                        value={nsMatchMode}
                        onChange={(e) => setNsMatchMode(e.target.value as 'all' | 'any')}
                      >
                        <option value="all">all (AND)</option>
                        <option value="any">any (OR)</option>
                      </select>
                    </label>
                    <label className="field">
                      <span>Priority</span>
                      <input
                        type="number"
                        value={nsPriority}
                        onChange={(e) => setNsPriority(Number(e.target.value))}
                      />
                    </label>
                  </div>
                  <div className="btn-row">
                    <button
                      type="button"
                      className="btn"
                      disabled={busy || !nsLabel.trim() || !nsConfigId}
                      onClick={() => void onCreateNamespace()}
                    >
                      Create namespace
                    </button>
                  </div>
                </section>

                <section className="panel">
                  <h2>Namespaces</h2>
                  {namespaces.length === 0 ? (
                    <p className="muted">No namespaces — create a config first, then a namespace.</p>
                  ) : (
                    <div className="table-wrap">
                      <table className="data-table">
                        <thead>
                          <tr>
                            <th>Label</th>
                            <th>Upstream</th>
                            <th>Algorithm</th>
                            <th>Mode</th>
                            <th>Pri</th>
                            <th />
                          </tr>
                        </thead>
                        <tbody>
                          {namespaces.map((n) => (
                            <tr
                              key={n.id}
                              className={`data-table__row${selectedNs === n.id ? ' data-table__row--active' : ''}`}
                              onClick={() => setSelectedNs(n.id)}
                            >
                              <td>
                                <strong>{n.label}</strong>
                              </td>
                              <td>
                                <code className="data-table__target">{n.upstreamHost}</code>
                              </td>
                              <td>
                                <code>{n.algorithm}</code>
                              </td>
                              <td>{n.matchMode}</td>
                              <td>{n.priority}</td>
                              <td
                                className="data-table__actions"
                                onClick={(e) => e.stopPropagation()}
                              >
                                <button
                                  type="button"
                                  className="btn btn--ghost btn--tiny"
                                  onClick={() => setSelectedNs(n.id)}
                                >
                                  Rules
                                </button>
                                <button
                                  type="button"
                                  className="btn btn--ghost btn--tiny"
                                  disabled={busy}
                                  onClick={() => void onDeleteNamespace(n.id)}
                                >
                                  Delete
                                </button>
                              </td>
                            </tr>
                          ))}
                        </tbody>
                      </table>
                    </div>
                  )}
                </section>
              </div>

              <section className="panel">
                <h2>Rules {selected ? `· ${selected.label}` : ''}</h2>
                {!selected ? (
                  <p className="muted">Select a namespace to manage match rules.</p>
                ) : (
                  <div className="split">
                    <div>
                      <p className="muted">
                        Match mode <code>{selected.matchMode}</code> —{' '}
                        {selected.matchMode === 'all'
                          ? 'every rule must match'
                          : 'any one rule selects the namespace'}
                        .
                      </p>
                      <label className="field">
                        <span>Kind</span>
                        <select
                          value={ruleKind}
                          onChange={(e) => setRuleKind(e.target.value as RuleMatch['kind'])}
                        >
                          <option value="header">header</option>
                          <option value="path">path</option>
                          <option value="method">method</option>
                          <option value="path_attr">path_attr</option>
                          <option value="regex">regex</option>
                        </select>
                      </label>
                      {ruleKind === 'header' || ruleKind === 'method' || ruleKind === 'path' ? (
                        <>
                          {ruleKind === 'header' ? (
                            <label className="field">
                              <span>Header name</span>
                              <input
                                value={ruleName}
                                onChange={(e) => setRuleName(e.target.value)}
                              />
                            </label>
                          ) : null}
                          <label className="field">
                            <span>Op</span>
                            <select value={ruleOp} onChange={(e) => setRuleOp(e.target.value)}>
                              <option value="eq">eq</option>
                              {ruleKind === 'path' ? (
                                <option value="prefix">prefix</option>
                              ) : null}
                              {ruleKind !== 'path' ? <option value="in">in</option> : null}
                            </select>
                          </label>
                          <label className="field">
                            <span>Value</span>
                            <input
                              value={ruleValue}
                              onChange={(e) => setRuleValue(e.target.value)}
                            />
                          </label>
                        </>
                      ) : null}
                      {ruleKind === 'path_attr' ? (
                        <>
                          <label className="field">
                            <span>Pattern</span>
                            <input
                              value={rulePattern}
                              onChange={(e) => setRulePattern(e.target.value)}
                            />
                          </label>
                          <label className="field">
                            <span>Attr</span>
                            <input
                              value={ruleAttr}
                              onChange={(e) => setRuleAttr(e.target.value)}
                            />
                          </label>
                          <label className="field">
                            <span>Value</span>
                            <input
                              value={ruleValue}
                              onChange={(e) => setRuleValue(e.target.value)}
                            />
                          </label>
                        </>
                      ) : null}
                      {ruleKind === 'regex' ? (
                        <>
                          <label className="field">
                            <span>Field</span>
                            <select
                              value={ruleField}
                              onChange={(e) => setRuleField(e.target.value)}
                            >
                              <option value="path">path</option>
                              <option value="full">full</option>
                            </select>
                          </label>
                          <label className="field">
                            <span>Pattern</span>
                            <input
                              value={rulePattern}
                              onChange={(e) => setRulePattern(e.target.value)}
                            />
                          </label>
                        </>
                      ) : null}
                      <div className="btn-row">
                        <button
                          type="button"
                          className="btn"
                          disabled={busy}
                          onClick={() => void onCreateRule()}
                        >
                          Add rule
                        </button>
                      </div>
                    </div>
                    <div>
                      {rules.length === 0 ? (
                        <p className="muted">No rules on this namespace.</p>
                      ) : (
                        <ul className="rule-list">
                          {rules.map((r) => (
                            <li key={r.id} className="rule-list__item">
                              <div>
                                <code>{formatMatch(r.match)}</code>
                                {r.label ? <span className="muted"> · {r.label}</span> : null}
                              </div>
                              <button
                                type="button"
                                className="btn btn--ghost btn--tiny"
                                disabled={busy}
                                onClick={() => void onDeleteRule(r.id)}
                              >
                                Delete
                              </button>
                            </li>
                          ))}
                        </ul>
                      )}
                    </div>
                  </div>
                )}
              </section>
            </>
          ) : null}
    </div>
  )
}
