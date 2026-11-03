import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import App from './App'
import './index.css'
import { applyThemePreference, readThemePreference } from './app/theme'

// Resolve the persisted or system theme before React mounts to avoid a
// light/dark flash on client-side navigation and page reload.
applyThemePreference(readThemePreference(), window.matchMedia('(prefers-color-scheme: dark)').matches)

const queryClient = new QueryClient({
  defaultOptions: {
    queries: { retry: false, refetchOnWindowFocus: false },
  },
})

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <App />
    </QueryClientProvider>
  </StrictMode>,
)
