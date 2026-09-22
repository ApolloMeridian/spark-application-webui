import { apiUrl, runtimeConfig } from './runtimeConfig';
import type { CreateUserInput, UpdateUserInput, UserAccount } from './types';

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const controller = new AbortController();
  const timeout = window.setTimeout(() => controller.abort(), runtimeConfig.api.requestTimeoutMs);
  try {
    const response = await fetch(apiUrl(path), {
      ...init,
      credentials: 'include',
      signal: controller.signal,
      headers: { Accept: 'application/json', ...(init?.body ? { 'Content-Type': 'application/json' } : {}), ...init?.headers },
    });
    if (response.status === 401) window.dispatchEvent(new Event('spark-console:unauthorized'));
    if (!response.ok) {
      const detail = await response.json().catch(() => ({ message: response.statusText })) as { message?: string };
      throw new Error(detail.message || `API request failed (${response.status})`);
    }
    if (response.status === 204) return undefined as T;
    return await response.json() as T;
  } finally {
    window.clearTimeout(timeout);
  }
}

export const userService = {
  list: () => request<UserAccount[]>('/v1/users'),
  create: (input: CreateUserInput) => request<UserAccount>('/v1/users', { method: 'POST', body: JSON.stringify(input) }),
  update: (id: string, input: UpdateUserInput) => request<UserAccount>(`/v1/users/${encodeURIComponent(id)}`, { method: 'PATCH', body: JSON.stringify(input) }),
  delete: (id: string) => request<void>(`/v1/users/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  updateProfile: (input: { displayName?: string; email?: string; currentPassword?: string; newPassword?: string }) =>
    request<{ user: UserAccount; passwordChanged: boolean }>('/v1/profile', { method: 'PATCH', body: JSON.stringify(input) }),
};
