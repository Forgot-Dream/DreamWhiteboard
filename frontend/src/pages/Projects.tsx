import { FormEvent, useEffect, useMemo, useState } from 'react';
import { FolderKanban, LayoutGrid, Plus, Users } from 'lucide-react';
import { api, canManage, type Board, type Project, type ProjectMember, type ProjectRole, type User } from '../lib/api';
import { useI18n } from '../lib/i18n';

interface ProjectsProps {
  user: User;
  onOpenBoard: (board: Board, project: Project, role?: ProjectRole) => void;
}

export function Projects({ user, onOpenBoard }: ProjectsProps) {
  const { t, formatDateTime } = useI18n();
  const [projects, setProjects] = useState<Project[]>([]);
  const [selectedID, setSelectedID] = useState('');
  const [boards, setBoards] = useState<Board[]>([]);
  const [members, setMembers] = useState<ProjectMember[]>([]);
  const [users, setUsers] = useState<User[]>([]);
  const [projectName, setProjectName] = useState('');
  const [boardName, setBoardName] = useState('');
  const [memberUser, setMemberUser] = useState('');
  const [memberRole, setMemberRole] = useState<ProjectRole>('editor');
  const [error, setError] = useState('');

  const selected = projects.find((project) => project.id === selectedID);
  const myRole = useMemo(() => members.find((member) => member.user_id === user.id)?.role, [members, user.id]);

  async function loadProjects() {
    const next = await api<Project[]>('/api/projects');
    setProjects(next);
    if (!selectedID && next[0]) setSelectedID(next[0].id);
  }

  async function loadProject(projectID: string) {
    const [nextBoards, nextMembers] = await Promise.all([
      api<Board[]>(`/api/projects/${projectID}/boards`),
      api<ProjectMember[]>(`/api/projects/${projectID}/members`)
    ]);
    setBoards(nextBoards);
    setMembers(nextMembers);
    if (user.system_role === 'system_admin') {
      setUsers(await api<User[]>('/api/admin/users'));
    }
  }

  useEffect(() => {
    loadProjects().catch((err) => setError(err.message));
  }, []);

  useEffect(() => {
    if (selectedID) loadProject(selectedID).catch((err) => setError(err.message));
  }, [selectedID]);

  async function createProject(event: FormEvent) {
    event.preventDefault();
    setError('');
    try {
      const project = await api<Project>('/api/projects', {
        method: 'POST',
        body: JSON.stringify({ name: projectName })
      });
      setProjectName('');
      await loadProjects();
      setSelectedID(project.id);
    } catch (err) {
      setError(err instanceof Error ? err.message : t('projects.createProjectFailed'));
    }
  }

  async function createBoard(event: FormEvent) {
    event.preventDefault();
    if (!selected) return;
    const board = await api<Board>(`/api/projects/${selected.id}/boards`, {
      method: 'POST',
      body: JSON.stringify({ name: boardName })
    });
    setBoardName('');
    setBoards((current) => [board, ...current]);
  }

  async function addMember(event: FormEvent) {
    event.preventDefault();
    if (!selected) return;
    await api<ProjectMember>(`/api/projects/${selected.id}/members`, {
      method: 'POST',
      body: JSON.stringify({ user_id: memberUser, role: memberRole })
    });
    await loadProject(selected.id);
  }

  return (
    <div className="workspace-grid">
      <aside className="sidebar panel">
        <div className="section-head">
          <h2>{t('projects.title')}</h2>
          <FolderKanban size={20} />
        </div>
        <form className="stack-form" onSubmit={createProject}>
          <input placeholder={t('projects.newProject')} value={projectName} onChange={(event) => setProjectName(event.target.value)} />
          <button className="primary" type="submit">
            <Plus size={16} /> {t('projects.create')}
          </button>
        </form>
        <div className="nav-list">
          {projects.map((project) => (
            <button className={project.id === selectedID ? 'active' : ''} key={project.id} onClick={() => setSelectedID(project.id)}>
              {project.name}
            </button>
          ))}
        </div>
      </aside>

      <section className="panel">
        <div className="section-head">
          <div>
            <h2>{selected?.name ?? t('projects.boardsFallback')}</h2>
            <p>{selected?.description || t('projects.boardsDescription')}</p>
          </div>
          <LayoutGrid size={22} />
        </div>
        <form className="inline-form" onSubmit={createBoard}>
          <input placeholder={t('projects.boardName')} value={boardName} onChange={(event) => setBoardName(event.target.value)} disabled={!selected} />
          <button className="primary" type="submit" disabled={!selected}>
            <Plus size={16} /> {t('projects.board')}
          </button>
        </form>
        {error && <p className="error">{error}</p>}
        <div className="card-grid">
          {boards.map((board) => (
            <button className="board-card" key={board.id} onClick={() => selected && onOpenBoard(board, selected, myRole)}>
              <strong>{board.name}</strong>
              <span>{t('projects.updatedAt', { version: board.version, time: formatDateTime(board.updated_at) })}</span>
            </button>
          ))}
        </div>
      </section>

      <section className="panel">
        <div className="section-head">
          <h2>{t('projects.members')}</h2>
          <Users size={20} />
        </div>
        {canManage(myRole, user) && (
          <form className="inline-form member-form" onSubmit={addMember}>
            <select value={memberUser} onChange={(event) => setMemberUser(event.target.value)}>
              <option value="">{t('projects.selectUser')}</option>
              {users.map((nextUser) => (
                <option key={nextUser.id} value={nextUser.id}>{nextUser.email}</option>
              ))}
            </select>
            <select value={memberRole} onChange={(event) => setMemberRole(event.target.value as ProjectRole)}>
              <option value="admin">{t('role.admin')}</option>
              <option value="editor">{t('role.editor')}</option>
              <option value="viewer">{t('role.viewer')}</option>
            </select>
            <button className="primary" type="submit">
              <Plus size={16} /> {t('projects.add')}
            </button>
          </form>
        )}
        <div className="table compact">
          {members.map((member) => (
            <div className="table-row" key={member.user_id}>
              <span>{member.user?.email ?? member.user_id}</span>
              <span className="badge">{t(`role.${member.role}`)}</span>
            </div>
          ))}
        </div>
      </section>
    </div>
  );
}
