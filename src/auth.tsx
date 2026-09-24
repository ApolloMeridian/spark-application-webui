import { createContext, useContext, useEffect, useMemo, useState, type ReactNode } from 'react';
import { apiUrl, runtimeConfig } from './runtimeConfig';
import type { UserAccount, UserRole } from './types';

export type Session = UserAccount;
interface AuthValue {
  session: Session | null;
  loading: boolean;
  login: (username: string, password: string) => Promise<void>;
  loginMock: (username: string, role: UserRole) => void;
  startOidcLogin: () => void;
  logout: () => Promise<void>;
  setRole: (role: UserRole) => void;
  updateSession: (user: UserAccount | null) => void;
}

const AuthContext = createContext<AuthValue | null>(null);

function mockSession(username: string, role: UserRole): Session {
  const now = new Date().toISOString();
  return { id: `mock-${username}`, username, displayName: username, email: '', role, namespaces: [], authSource: 'mock', disabled: false, createdAt: now, updatedAt: now };
}

export function AuthProvider({ children }: { children: ReactNode }) {
  const [session, setSession] = useState<Session | null>(() => {
    if (runtimeConfig.auth.mode !== 'mock') return null;
    const value = localStorage.getItem('spark-console-session');
    if (!value) return null;
    const parsed = JSON.parse(value) as Partial<Session>;
    return mockSession(parsed.username ?? 'demo', parsed.role ?? 'viewer');
  });
  const [loading, setLoading] = useState(runtimeConfig.auth.mode !== 'mock');

  useEffect(() => {
    if (runtimeConfig.auth.mode === 'mock') return;
    const controller = new AbortController();
    fetch(apiUrl(runtimeConfig.auth.oidc.sessionPath), { credentials: 'include', signal: controller.signal, headers: { Accept: 'application/json' } })
      .then(async (response) => { if (!response.ok) throw new Error('No active session'); setSession(await response.json() as Session); })
      .catch(() => setSession(null))
      .finally(() => setLoading(false));
    return () => controller.abort();
  }, []);

  useEffect(() => {
    const unauthorized = () => setSession(null);
    window.addEventListener('spark-console:unauthorized', unauthorized);
    return () => window.removeEventListener('spark-console:unauthorized', unauthorized);
  }, []);

  const value = useMemo<AuthValue>(() => ({
    session,
    loading,
    login: async (username, password) => {
      const response = await fetch(apiUrl(runtimeConfig.auth.oidc.loginPath), {
        method: 'POST', credentials: 'include', headers: { Accept: 'application/json', 'Content-Type': 'application/json' },
        body: JSON.stringify({ username, password }),
      });
      if (!response.ok) {
        const detail = await response.json().catch(() => ({ message: response.statusText })) as { message?: string };
        throw new Error(detail.message || 'Login failed');
      }
      setSession(await response.json() as Session);
    },
    loginMock: (username, role) => {
      const next = mockSession(username, role);
      localStorage.setItem('spark-console-session', JSON.stringify(next)); setSession(next);
    },
    startOidcLogin: () => {
      window.location.assign(`${apiUrl(runtimeConfig.auth.oidc.authorizePath)}?returnUrl=${encodeURIComponent('/overview')}`);
    },
    logout: async () => {
      if (runtimeConfig.auth.mode === 'mock') {
        localStorage.removeItem('spark-console-session'); setSession(null); return;
      }
      await fetch(apiUrl(runtimeConfig.auth.oidc.logoutPath), { method: 'POST', credentials: 'include', headers: { Accept: 'application/json' } }).catch(() => undefined);
      setSession(null);
    },
    setRole: (role) => setSession((current) => {
      if (!current || runtimeConfig.auth.mode !== 'mock') return current;
      const next = { ...current, role };
      localStorage.setItem('spark-console-session', JSON.stringify(next));
      return next;
    }),
    updateSession: setSession,
  }), [loading, session]);
  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth() {
  const value = useContext(AuthContext);
  if (!value) throw new Error('useAuth must be used inside AuthProvider');
  return value;
}
