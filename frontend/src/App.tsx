import { useEffect, useState } from 'react';
import { LogOut, Shield, SquareStack } from 'lucide-react';
import { Login } from './pages/Login';
import { Projects } from './pages/Projects';
import { Admin } from './pages/Admin';
import { BoardEditor } from './pages/BoardEditor';
import { api, setToken, type Board, type Project, type ProjectRole, type User } from './lib/api';

type View = 'projects' | 'admin' | 'board';

export function App() {
  const [user, setUser] = useState<User | null>(null);
  const [view, setView] = useState<View>('projects');
  const [active, setActive] = useState<{ board: Board; project: Project; role?: ProjectRole } | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    api<User>('/api/me')
      .then(setUser)
      .catch(() => setToken(''))
      .finally(() => setLoading(false));
  }, []);

  if (loading) return <div className="loading">Loading</div>;
  if (!user) return <Login onLogin={setUser} />;
  if (view === 'board' && active) {
    return <BoardEditor user={user} board={active.board} project={active.project} role={active.role} onBack={() => setView('projects')} />;
  }

  async function logout() {
    await api('/api/auth/logout', { method: 'POST' }).catch(() => undefined);
    setToken('');
    setUser(null);
  }

  return (
    <div className="app-shell">
      <header className="app-header">
        <div className="brand">
          <SquareStack size={24} />
          <div>
            <strong>DreamWhiteboard</strong>
            <span>{user.email}</span>
          </div>
        </div>
        <nav>
          <button className={view === 'projects' ? 'active' : ''} onClick={() => setView('projects')}>Projects</button>
          {user.system_role === 'system_admin' && (
            <button className={view === 'admin' ? 'active' : ''} onClick={() => setView('admin')}>
              <Shield size={16} /> Admin
            </button>
          )}
          <button onClick={logout}><LogOut size={16} /> Sign out</button>
        </nav>
      </header>
      <main className="app-main">
        {view === 'projects' && (
          <Projects
            user={user}
            onOpenBoard={(board, project, role) => {
              setActive({ board, project, role });
              setView('board');
            }}
          />
        )}
        {view === 'admin' && <Admin />}
      </main>
    </div>
  );
}

