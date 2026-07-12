import { useEffect } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { LogOut, Shield, SquareStack } from 'lucide-react';
import { Navigate, NavLink, Outlet, Route, Routes, useLocation, useNavigate } from 'react-router-dom';
import { api, APIError, AUTH_EXPIRED_EVENT, type User } from './lib/api';
import { LocaleSelect, useI18n } from './lib/i18n';
import { Admin } from './pages/Admin';
import { BoardEditor } from './pages/BoardEditor';
import { ChangePassword } from './pages/ChangePassword';
import { Login } from './pages/Login';
import { Projects } from './pages/Projects';

export const authQueryKey = ['auth', 'me'] as const;

export function App() {
  const { t } = useI18n();
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const auth = useQuery({
    queryKey: authQueryKey,
    queryFn: () => api<User>('/api/me'),
    retry: (count, error) => !(error instanceof APIError && error.status === 401) && count < 1
  });

  useEffect(() => {
    const expired = () => {
      queryClient.clear();
      queryClient.setQueryData<User | null>(authQueryKey, null);
      navigate('/login', { replace: true });
    };
    window.addEventListener(AUTH_EXPIRED_EVENT, expired);
    return () => window.removeEventListener(AUTH_EXPIRED_EVENT, expired);
  }, [navigate, queryClient]);

  if (auth.isLoading) return <div className="loading">{t('app.loading')}…</div>;

  return (
    <Routes>
      <Route path="/login" element={auth.data ? <Navigate to="/projects" replace /> : <Login />} />
      <Route element={<RequireAuth user={auth.data} />}>
        <Route path="/change-password" element={<ChangePassword user={auth.data!} />} />
        <Route element={<RequirePasswordChanged user={auth.data!} />}>
          <Route element={<AppLayout user={auth.data!} />}>
            <Route path="/projects" element={<Projects user={auth.data!} />} />
            <Route path="/projects/:projectId" element={<Projects user={auth.data!} />} />
            <Route path="/admin" element={auth.data?.system_role === 'system_admin' ? <Admin /> : <Navigate to="/projects" replace />} />
          </Route>
          <Route path="/boards/:boardId" element={<BoardEditor user={auth.data!} />} />
        </Route>
      </Route>
      <Route path="*" element={<Navigate to={auth.data ? '/projects' : '/login'} replace />} />
    </Routes>
  );
}

function RequireAuth({ user }: { user?: User }) {
  const location = useLocation();
  if (!user) return <Navigate to="/login" replace state={{ from: location.pathname + location.search }} />;
  return <Outlet />;
}

function RequirePasswordChanged({ user }: { user: User }) {
  if (user.must_change_password) return <Navigate to="/change-password" replace />;
  return <Outlet />;
}

function AppLayout({ user }: { user: User }) {
  const { t } = useI18n();
  const navigate = useNavigate();
  const queryClient = useQueryClient();

  async function logout() {
    await api('/api/auth/logout', { method: 'POST' }).catch(() => undefined);
    queryClient.clear();
    queryClient.setQueryData<User | null>(authQueryKey, null);
    navigate('/login', { replace: true });
  }

  return (
    <div className="app-shell">
      <header className="app-header">
        <NavLink className="brand" to="/projects">
          <SquareStack size={24} />
          <div><strong>DreamWhiteboard</strong><span>{user.email}</span></div>
        </NavLink>
        <nav>
          <NavLink to="/projects">{t('nav.projects')}</NavLink>
          {user.system_role === 'system_admin' && <NavLink to="/admin"><Shield size={16} /> {t('nav.admin')}</NavLink>}
          <LocaleSelect />
          <button onClick={logout}><LogOut size={16} /> {t('nav.signOut')}</button>
        </nav>
      </header>
      <main className="app-main"><Outlet /></main>
    </div>
  );
}
