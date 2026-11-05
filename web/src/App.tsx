import { Suspense, lazy } from 'react'
import { BrowserRouter, Navigate, Route, Routes, useLocation } from 'react-router-dom'
import { AppShell } from '@/components/layout/AppShell'
import { SetupGate } from '@/components/layout/SetupGate'
import { LoginGate } from '@/components/layout/LoginGate'
import { ROUTES } from '@/lib/routes'
import { allRedirects } from '@/lib/routeManifest'
import { I18nProvider } from '@/lib/i18n'

// Onboarding renders standalone: there is no sidebar to wander off into before
// the agent can answer anything.
const SetupPage = lazy(() => import('@/pages/SetupPage'))
// The dashboard login renders standalone too, before any chrome loads.
const LoginPage = lazy(() => import('@/pages/LoginPage'))

// Dev-only UI prototypes live under src/sample/ (gitignored, never shipped).
// The glob is guarded by import.meta.env.DEV so production bundles never even
// see the folder — Vite tree-shakes the branch out and the prototypes stay
// out of the shipped app. In dev, each sample/<name>.tsx mounts standalone at
// /sample/<name>; on a clean checkout the glob resolves to nothing and
// /sample simply 404s to "/".
const sampleRoutes = import.meta.env.DEV
  ? Object.entries(import.meta.glob('./sample/*.tsx')).map(([path, loader]) => {
      const name = path.replace('./sample/', '').replace('.tsx', '')
      const Comp = lazy(loader as () => Promise<{ default: React.ComponentType }>)
      return { name, Comp }
    })
  : []

/**
 * Sends a retired path (or a bare hub root) to its canonical page. Query and
 * hash ride along so old bookmarks like /vps?x=1#y keep their state, and the
 * history entry is replaced so Back does not bounce through the old URL.
 */
function LegacyRedirect({ to }: { to: string }) {
  const { search, hash } = useLocation()
  return <Navigate to={{ pathname: to, search, hash }} replace />
}

const REDIRECTS = allRedirects()

export function App() {
  return (
    <I18nProvider>
      <BrowserRouter>
        <Routes>
          <Route
            path="/setup"
            element={
              <Suspense fallback={null}>
                <SetupPage />
              </Suspense>
            }
          />
          <Route
            path="/login"
            element={
              <Suspense fallback={null}>
                <LoginPage />
              </Suspense>
            }
          />
          {/* Dev-only prototype routes, standalone (no chrome, no gates). */}
          {sampleRoutes.map(({ name, Comp }) => (
            <Route
              key={name}
              path={`/sample/${name}`}
              element={
                <Suspense fallback={null}>
                  <Comp />
                </Suspense>
              }
            />
          ))}

          {/* LoginGate holds everything behind the dashboard password (when
              set); SetupGate redirects a fresh install to onboarding; AppShell
              owns the page container, header, and Suspense boundary, so every
              route inside renders with identical chrome. */}
          <Route element={<LoginGate />}>
            <Route element={<SetupGate />}>
              <Route element={<AppShell />}>
                {ROUTES.flatMap(({ path, aliases, component: Component }) =>
                  [path, ...(aliases ?? [])].map((p) => (
                    <Route key={p} path={p} element={<Component />} />
                  )),
                )}
                {REDIRECTS.map(({ from, to }) => (
                  <Route key={from} path={from} element={<LegacyRedirect to={to} />} />
                ))}
                <Route path="*" element={<Navigate to="/" replace />} />
              </Route>
            </Route>
          </Route>
        </Routes>
      </BrowserRouter>
    </I18nProvider>
  )
}
