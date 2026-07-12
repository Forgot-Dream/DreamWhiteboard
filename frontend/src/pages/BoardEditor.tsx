import { useMemo } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useNavigate, useParams } from 'react-router-dom';
import { api, canEdit as canUserEdit, canManage as canUserManage, type BoardAccess, type Project, type ProjectMember, type ProjectRole, type User } from '../lib/api';
import { BoardToolbar } from '../board/BoardToolbar';
import { CanvasViewport } from '../board/CanvasViewport';
import { useBoardImageUpload } from '../board/imageUpload';
import { useBoardKeyboard } from '../board/keyboard';
import { useBoardRuntime } from '../board/runtime';
import { useI18n } from '../lib/i18n';

export function BoardEditor({ user }: { user: User }) {
  const { errorMessage, t } = useI18n();
  const { boardId = '' } = useParams();
  const navigate = useNavigate();
  const access = useQuery({
    queryKey: ['boards', boardId],
    queryFn: () => api<BoardAccess>(`/api/boards/${encodeURIComponent(boardId)}`),
    enabled: Boolean(boardId),
    retry: 1
  });
  const projectID = access.data?.board.project_id ?? '';
  const project = useQuery({
    queryKey: ['projects', projectID],
    queryFn: () => api<Project>(`/api/projects/${encodeURIComponent(projectID)}`),
    enabled: Boolean(projectID) && !access.data?.project
  });
  const members = useQuery({
    queryKey: ['projects', projectID, 'members'],
    queryFn: () => api<ProjectMember[]>(`/api/projects/${encodeURIComponent(projectID)}/members`),
    enabled: Boolean(projectID) && access.data?.permission?.role === undefined && user.system_role !== 'system_admin'
  });
  const role = access.data?.permission?.role ?? members.data?.find((member) => member.user_id === user.id)?.role;

  if (access.isLoading) return <div className="loading">{t('editor.loadingBoard')}</div>;
  if (access.error || !access.data) return <div className="loading error-page"><div><h2>{t('editor.unableToOpen')}</h2><p>{access.error ? errorMessage(access.error) : t('editor.boardNotFound')}</p><button className="primary" onClick={() => navigate('/projects')}>{t('editor.backToProjects')}</button></div></div>;

  return (
    <BoardWorkspace
      key={boardId}
      access={access.data}
      project={access.data.project ?? project.data}
      role={role}
      user={user}
    />
  );
}

function BoardWorkspace({ access, project, role, user }: { access: BoardAccess; project?: Project; role?: ProjectRole; user: User }) {
  const editable = useMemo(() => canUserEdit(role, user, access.permission?.can_edit), [access.permission?.can_edit, role, user]);
  const manageable = useMemo(() => access.permission?.can_manage ?? canUserManage(role, user), [access.permission?.can_manage, role, user]);
  const runtime = useBoardRuntime(access.board.id, access.board.project_id, user, editable, manageable);
  const imageUpload = useBoardImageUpload(runtime);
  useBoardKeyboard(runtime, (file) => imageUpload.upload(file) !== undefined);
  return (
    <div className="editor-shell">
      <BoardToolbar runtime={runtime} board={access.board} project={project} onBackProjectID={access.board.project_id} imageUpload={imageUpload} />
      <CanvasViewport runtime={runtime} />
    </div>
  );
}
