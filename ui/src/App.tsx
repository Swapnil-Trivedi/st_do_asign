import { BrowserRouter, Navigate, Route, Routes } from 'react-router-dom'
import { Sidebar } from './components/Sidebar'
import { ControlPlane } from './pages/ControlPlane'
import { MockBackend } from './pages/MockBackend'
import { Overview } from './pages/Overview'
import { VegetaClient } from './pages/VegetaClient'
import './App.css'

export default function App() {
  return (
    <BrowserRouter>
      <div className="shell">
        <Sidebar />
        <main className="shell__main">
          <Routes>
            <Route path="/" element={<Overview />} />
            <Route path="/mock-backend" element={<MockBackend />} />
            <Route path="/vegeta" element={<VegetaClient />} />
            <Route path="/control-plane" element={<ControlPlane />} />
            <Route path="*" element={<Navigate to="/" replace />} />
          </Routes>
        </main>
      </div>
    </BrowserRouter>
  )
}
