// A local, presentation-only preference. Never tied to a Qual Principal or permission.
export const THEME_STORAGE_KEY = 'qual-theme'

export type ThemePreference = 'system' | 'light' | 'dark'

export function parseThemePreference(value: string | null): ThemePreference {
  return value === 'light' || value === 'dark' ? value : 'system'
}

export function resolveTheme(preference: ThemePreference, systemDark: boolean): 'light' | 'dark' {
  return preference === 'system' ? (systemDark ? 'dark' : 'light') : preference
}

export function readThemePreference(): ThemePreference {
  try {
    return parseThemePreference(window.localStorage.getItem(THEME_STORAGE_KEY))
  } catch {
    // Private browsing or storage policy can deny localStorage.
    return 'system'
  }
}

export function saveThemePreference(preference: ThemePreference): void {
  try {
    window.localStorage.setItem(THEME_STORAGE_KEY, preference)
  } catch {
    // A usable theme selector must not depend on persistent storage.
  }
}

export function applyThemePreference(preference: ThemePreference, systemDark: boolean): void {
  const theme = resolveTheme(preference, systemDark)
  document.documentElement.classList.toggle('dark', theme === 'dark')
  document.documentElement.style.colorScheme = theme
}
