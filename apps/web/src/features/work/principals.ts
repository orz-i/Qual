import { useQuery } from '@tanstack/react-query'
import type { components } from '@qual/sdk-ts'
import { api, apiErrorMessage } from '@/lib/qual-api'

export type ProjectPrincipalSummary = components['schemas']['ProjectPrincipalSummary']
const principalKey = (projectID: string) => ['project-principals', projectID] as const

export function useProjectPrincipals(projectID: string, knownIDs: (string | null | undefined)[]) {
  const ids = [...new Set(knownIDs.filter((id): id is string => Boolean(id)))].sort()
  const query = useQuery({
    queryKey: [...principalKey(projectID), ids.join(',')],
    enabled: Boolean(projectID && ids.length),
    retry: false,
    staleTime: 20_000,
    queryFn: async () => {
      const batches: ProjectPrincipalSummary[][] = await Promise.all(
        Array.from({ length: Math.ceil(ids.length / 50) }, (_, n) => ids.slice(n * 50, n * 50 + 50))
          .map(async (batch) => {
            const { data, error } = await api.GET('/api/v1/projects/{projectID}/principals', {
              params: { path: { projectID }, query: { ids: batch.join(',') } },
            })
            if (error || !data) throw new Error(apiErrorMessage(error))
            return data.items
          }),
      )
      return batches.flat()
    },
  })
  const byID = new Map(query.data?.map((principal) => [principal.id, principal]))
  return { ...query, byID }
}

const kindNames: Record<ProjectPrincipalSummary['kind'], string> = {
  human: 'Human',
  agent: 'Agent',
  integration: 'Integration',
}

/** An unknown or revoked principal remains auditable by its real ID. */
export function principalLabel(id: string | null | undefined, info?: ProjectPrincipalSummary, compact = false) {
  if (!id) return '未分配'
  if (info?.id === id) return `${info.display_name} · ${kindNames[info.kind]}`
  return compact ? `身份未解析 · ${id.slice(0, 8)}…` : `身份未解析 · ${id}`
}
