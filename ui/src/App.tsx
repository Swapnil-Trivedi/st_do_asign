import { BrowserRouter, Navigate, Route, Routes, useLocation } from 'react-router-dom'
import { JobBanner } from './components/JobBanner'
import { Sidebar } from './components/Sidebar'
import { MockBackend } from './pages/MockBackend'
import { Overview } from './pages/Overview'
import { RateLimiter } from './pages/RateLimiter'
import { Testing } from './pages/Testing'
import { VegetaClient } from './pages/VegetaClient'
import { PipelineProvider } from './state/PipelineContext'
import './App.css'

/** Keep micro-app pages mounted so local UI state survives sidebar navigation. */
function PersistentPages() {
  const { pathname } = useLocation()
  const show = (path: string) =>
    pathname === path ? 'page-slot' : 'page-slot page-slot--hidden'

  return (
    <>
      <div className={show('/')} aria-hidden={pathname !== '/'}>
        <Overview />
      </div>
      <div className={show('/rate-limiter')} aria-hidden={pathname !== '/rate-limiter'}>
        <RateLimiter />
      </div>
      <div className={show('/testing')} aria-hidden={pathname !== '/testing'}>
        <Testing />
      </div>
      <div className={show('/mock-backend')} aria-hidden={pathname !== '/mock-backend'}>
        <MockBackend />
      </div>
      <div className={show('/vegeta')} aria-hidden={pathname !== '/vegeta'}>
        <VegetaClient />
      </div>
      <Routes>
        <Route path="/" element={null} />
        <Route path="/rate-limiter" element={null} />
        <Route path="/testing" element={null} />
        <Route path="/mock-backend" element={null} />
        <Route path="/vegeta" element={null} />
        <Route path="/control-plane" element={<Navigate to="/rate-limiter" replace />} />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
    </>
  )
}

export default function App() {
  return (
    <BrowserRouter>
      <PipelineProvider>
        <div className="shell">
          <Sidebar />
          <div className="shell__content">
            <JobBanner />
            <main className="shell__main">
              <PersistentPages />
            </main>
          </div>
        </div>
      </PipelineProvider>
    </BrowserRouter>
  )
}
