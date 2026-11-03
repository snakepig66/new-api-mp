import { api } from '@/legacy/lib/api'

export interface ApiResponse<T = unknown> {
  success: boolean
  message?: string
  data?: T
}

export async function getGroups(): Promise<ApiResponse<string[]>> {
  const res = await api.get('/api/group/')
  return res.data
}
