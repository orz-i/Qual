import { createBrowserRouter } from 'react-router'
import { AppLayout } from '@/app/layout'
import { HomePage, NotFoundPage } from '@/features/overview/pages'
import { LoginPage, RequireSession } from '@/features/auth/pages'

export const router = createBrowserRouter([
  {
    path: '/',
    element: <AppLayout />,
    children: [
      { index: true, element: <HomePage /> },
      { path: 'login', element: <LoginPage /> },
      {
        element: <RequireSession />,
        children: [
          { path: 'projects', lazy: () => import('@/features/work/board').then((m) => ({ Component: m.ProjectsPage })) },
          { path: 'projects/:projectID', lazy: () => import('@/features/work/board').then((m) => ({ Component: m.WorkBoardPage })) },
          { path: 'projects/:projectID/items/:itemID', lazy: () => import('@/features/work/detail').then((m) => ({ Component: m.WorkDetailPage })) },
          { path: 'projects/:projectID/activity', lazy: () => import('@/features/work/detail').then((m) => ({ Component: m.ProjectActivityPage })) },
          { path: 'projects/:projectID/reviews', lazy: () => import('@/features/reviews/pages').then((m) => ({ Component: m.ReviewCenterPage })) },
          { path: 'projects/:projectID/access', lazy: () => import('@/features/access/page').then((m) => ({ Component: m.ProjectAccessPage })) },
          { path: 'projects/:projectID/archive', lazy: () => import('@/features/evidence/archive').then((m) => ({ Component: m.ArchivePage })) },
          { path: 'projects/:projectID/webhooks', lazy: () => import('@/features/evidence/webhooks').then((m) => ({ Component: m.WebhookPage })) },
          { path: 'tests', lazy: () => import('@/features/tests/cases').then((m) => ({ Component: m.TestsHomePage })) },
          { path: 'projects/:projectID/tests/cases', lazy: () => import('@/features/tests/cases').then((m) => ({ Component: m.TestCasesPage })) },
          { path: 'projects/:projectID/tests/cases/:caseID', lazy: () => import('@/features/tests/cases').then((m) => ({ Component: m.TestCaseDetailPage })) },
          { path: 'projects/:projectID/tests/runs', lazy: () => import('@/features/tests/runs').then((m) => ({ Component: m.TestRunsPage })) },
          { path: 'projects/:projectID/tests/runs/:runID', lazy: () => import('@/features/tests/run-detail').then((m) => ({ Component: m.TestRunDetailPage })) },
        ],
      },
      { path: '*', element: <NotFoundPage /> },
    ],
  },
])
