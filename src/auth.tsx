import { createContext, useContext, useEffect, useMemo, useState, type ReactNode } from 'react';
import { apiUrl, runtimeConfig } from './runtimeConfig';
import type { UserRole } from './types';

export interface Session { username: string; role: UserRole }
interface AuthValue { session: Session | null; loading: boolean; login: (session: Session) => void; startOidcLogin: () => void; logout: () => void; setRole: (role: UserRole) => void }
const AuthContext = createContext<AuthValue | null>(null);

export function AuthProvider({ children }: { children: ReactNode }) {
  const [session, setSession] = useState<Session | null>(() => {
    if (runtimeConfig.auth.mode === 'oidc') return null;
    const value = localStorage.getItem('spark-console-session');
    return value ? JSON.parse(value) : null;
  });
  const [loading, setLoading] = useState(runtimeConfig.auth.mode === 'oidc');
  useEffect(() => {
    if (runtimeConfig.auth.mode !== 'oidc') return;
    const controller = new AbortController();
    fetch(apiUrl(runtimeConfig.auth.oidc.sessionPath), { credentials: 'include', signal: controller.signal, headers: { Accept: 'application/json' } })
      .then(async (response) => { if (!response.ok) throw new Error('No active OIDC session'); setSession(await response.json() as Session); })
      .catch(() => setSession(null))
      .finally(() => setLoading(false));
    return () => controller.abort();
  }, []);
  const value = useMemo<AuthValue>(() => ({
    session,
    loading,
    login: (next) => { localStorage.setItem('spark-console-session', JSON.stringify(next)); setSession(next); },
    startOidcLogin: () => {
      const returnUrl = `${window.location.origin}/overview`;
      window.location.assign(`${apiUrl(runtimeConfig.auth.oidc.loginPath)}?returnUrl=${encodeURIComponent(returnUrl)}`);
    },
    logout: () => {
      if (runtimeConfig.auth.mode === 'oidc') {
        const returnUrl = `${window.location.origin}/login`;
        window.location.assign(`${apiUrl(runtimeConfig.auth.oidc.logoutPath)}?returnUrl=${encodeURIComponent(returnUrl)}`);
        return;
      }
      localStorage.removeItem('spark-console-session'); setSession(null);
    },
    setRole: (role) => setSession((current) => {
      if (!current) return current;
      const next = { ...current, role };
      localStorage.setItem('spark-console-session', JSON.stringify(next));
      return next;
    }),
  }), [loading, session]);
  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth() {
  const value = useContext(AuthContext);
  if (!value) throw new Error('useAuth must be used inside AuthProvider');
  return value;
}
