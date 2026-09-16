import { Navigate, Route, Routes } from 'react-router'
import { useAuth } from './auth'
import { Layout } from './components/Layout'
import { ConnectPage } from './pages/ConnectPage'
import { DashboardPage } from './pages/DashboardPage'
import { FrameworksPage } from './pages/FrameworksPage'
import { RequirementsPage } from './pages/RequirementsPage'
import { RequirementDetailPage } from './pages/RequirementDetailPage'
import { EvidencePage } from './pages/EvidencePage'
import { AssessmentPage } from './pages/AssessmentPage'

export default function App() {
  const { auth } = useAuth()
  if (!auth) return <ConnectPage />

  return (
    <Routes>
      <Route element={<Layout />}>
        <Route index element={<DashboardPage />} />
        <Route path="frameworks" element={<FrameworksPage />} />
        <Route path="frameworks/:frameworkId" element={<RequirementsPage />} />
        <Route path="requirements" element={<RequirementsPage />} />
        <Route path="requirements/:id" element={<RequirementDetailPage />} />
        <Route path="evidence" element={<EvidencePage />} />
        <Route path="assessment" element={<AssessmentPage />} />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Route>
    </Routes>
  )
}
