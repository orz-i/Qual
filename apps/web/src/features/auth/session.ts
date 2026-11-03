import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, apiErrorMessage } from '@/lib/qual-api'

export const sessionKey = ['browser-session'] as const

export async function fetchSession() {
  const { data, error, response } = await api.GET('/api/v1/auth/session')
  if (response.status === 401) return null
  if (error || !data) throw new Error(apiErrorMessage(error))
  return data
}

export function useSession() {
  return useQuery({ queryKey: sessionKey, queryFn: fetchSession, retry: false, staleTime: 30_000 })
}

export function useLogin() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: async (code: string) => {
      const { data, error } = await api.POST('/api/v1/auth/login', { body: { code } })
      if (error || !data) throw new Error(apiErrorMessage(error))
      return data
    },
    onSuccess: (data) => queryClient.setQueryData(sessionKey, data),
  })
}

export function useLogout(csrfToken: string) {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: async () => {
      const { error, response } = await api.POST('/api/v1/auth/logout', {
        params: { header: { 'X-CSRF-Token': csrfToken } },
      })
      if (error || response.status !== 204) throw new Error(apiErrorMessage(error))
    },
    onSuccess: () => {
      queryClient.clear()
      queryClient.setQueryData(sessionKey, null)
    },
  })
}
