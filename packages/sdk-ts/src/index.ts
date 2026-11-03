import createClient from 'openapi-fetch'
import type { paths } from './schema'

/**
 * Transport client with types derived from packages/contracts/openapi.yaml.
 * Authentication and allowed resource scopes belong to the server.
 */
export function createQualClient(baseUrl = '') {
  return createClient<paths>({ baseUrl })
}

export type { paths, components, operations } from './schema'
