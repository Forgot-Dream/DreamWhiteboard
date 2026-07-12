import { createContext, useContext, useEffect, useMemo, useState, type ReactNode } from 'react';
import { APIError } from './api';

export type Locale = 'en' | 'zh_cn';

const messages = {
  en: {
    'app.loading': 'Loading',
    'common.save': 'Save',
    'common.cancel': 'Cancel',
    'common.delete': 'Delete',
    'common.retry': 'Retry',
    'nav.projects': 'Projects',
    'nav.admin': 'Admin',
    'nav.signOut': 'Sign out',
    'locale.label': 'Language',
    'locale.en': 'English',
    'locale.zh_cn': '简体中文',

    'login.subtitle': 'Collaborative project whiteboards for structured blocks, assets, and live editing.',
    'login.email': 'Email',
    'login.password': 'Password',
    'login.signIn': 'Sign in',
    'login.failed': 'Login failed',

    'password.title': 'Change password',
    'password.subtitle': 'Choose a private password before continuing.',
    'password.current': 'Current password',
    'password.new': 'New password',
    'password.confirm': 'Confirm password',
    'password.mismatch': 'Passwords do not match.',
    'password.save': 'Save password',

    'admin.title': 'System users',
    'admin.subtitle': 'New users must replace the one-time password at first login.',
    'admin.tempPassword': 'Created users receive the temporary password {password}.',
    'admin.emailPlaceholder': 'email',
    'admin.namePlaceholder': 'name',
    'admin.passwordPlaceholder': 'one-time password',
    'admin.add': 'Add',
    'admin.createFailed': 'Create user failed',
    'admin.empty': 'No users yet.',
    'admin.systemRoleFor': 'System role for {email}',
    'admin.oneTimePasswordFor': 'One-time password for {email}',
    'admin.resetPassword': 'Reset password',
    'admin.role.user': 'user',
    'admin.role.system_admin': 'system_admin',

    'projects.title': 'Projects',
    'projects.newProject': 'New project',
    'projects.projectName': 'Project name',
    'projects.descriptionOptional': 'Description (optional)',
    'projects.create': 'Create',
    'projects.empty': 'Create your first project.',
    'projects.boardsFallback': 'Boards',
    'projects.boardsDescription': 'Create and open project whiteboards.',
    'projects.deleteProjectConfirm': 'Delete “{name}” and all of its boards and uploads?',
    'projects.boardName': 'Board name',
    'projects.board': 'Board',
    'projects.noBoards': 'No boards yet.',
    'projects.members': 'Members',
    'projects.selectUser': 'Select user',
    'projects.userID': 'User ID',
    'projects.add': 'Add',
    'projects.noMembers': 'No members.',
    'projects.editProject': 'Edit project',
    'projects.description': 'Description',
    'projects.updated': 'Updated {time}',
    'projects.rename': 'Rename',
    'projects.deleteBoardConfirm': 'Delete “{name}”?',
    'projects.removeMember': 'Remove member',
    'projects.removeMemberConfirm': 'Remove this member?',
    'projects.createProjectFailed': 'Create project failed',
    'projects.updatedAt': 'v{version} · {time}',
    'role.owner': 'owner',
    'role.admin': 'admin',
    'role.editor': 'editor',
    'role.viewer': 'viewer',

    'editor.loadingBoard': 'Loading board…',
    'editor.unableToOpen': 'Unable to open board',
    'editor.boardNotFound': 'Board not found',
    'editor.backToProjects': 'Back to projects',
    'editor.projectFallback': 'Project',
    'editor.toolbarLabel': 'Whiteboard tools',
    'editor.saved': 'Saved {time}',
    'editor.saving': 'Saving…',
    'editor.connection.savingOne': 'saving {count} update',
    'editor.connection.savingMany': 'saving {count} updates',
    'editor.connection.synced': 'synced · seq {sequence}',
    'editor.connection.live': 'live',
    'editor.connection.connecting': 'connecting',
    'editor.connection.syncing': 'syncing',
    'editor.connection.offline': 'offline',
    'editor.back': 'Back',
    'editor.select': 'Select (V)',
    'editor.pan': 'Pan',
    'editor.textBox': 'Text box',
    'editor.textTool': 'Text',
    'editor.uploadImage': 'Upload image',
    'editor.undo': 'Undo',
    'editor.redo': 'Redo',
    'editor.duplicate': 'Duplicate',
    'editor.bringToFront': 'Bring to front',
    'editor.sendToBack': 'Send to back',
    'editor.moveForward': 'Move forward',
    'editor.moveBackward': 'Move backward',
    'editor.alignHorizontal': 'Align horizontal centers',
    'editor.alignVertical': 'Align vertical centers',
    'editor.distributeHorizontal': 'Distribute horizontally',
    'editor.distributeVertical': 'Distribute vertically',
    'editor.zoomOut': 'Zoom out',
    'editor.zoomIn': 'Zoom in',
    'editor.fitAll': 'Fit all',
    'editor.fitSelection': 'Fit selection',
    'editor.resetView': 'Reset view',
    'editor.delete': 'Delete',
    'editor.dragBlock': 'Drag block',
    'editor.fill': 'Fill',
    'editor.text': 'Text',
    'editor.border': 'Border',
    'editor.width': 'Width',
    'editor.noFill': 'No fill',
    'editor.aspectLock': 'Lock ratio',
    'editor.defaultText': 'Text',
    'editor.upload.invalidType': 'Choose a PNG, JPEG, or GIF image.',
    'editor.upload.tooLarge': 'Image exceeds the 25 MB upload limit.',
    'editor.upload.decodeFailed': 'The image could not be decoded.',
    'editor.upload.failed': 'Upload failed.',
    'editor.upload.progress': 'Uploading {file}: {progress}%',
    'editor.upload.cancel': 'Cancel',
    'editor.upload.retry': 'Retry',
    'editor.canvasStats': '{total} blocks · {rendered} rendered',
    'editor.moveTextBlock': 'Move text block',
    'editor.resize.n': 'Resize top edge',
    'editor.resize.ne': 'Resize top-right corner',
    'editor.resize.e': 'Resize right edge',
    'editor.resize.se': 'Resize bottom-right corner',
    'editor.resize.s': 'Resize bottom edge',
    'editor.resize.sw': 'Resize bottom-left corner',
    'editor.resize.w': 'Resize left edge',
    'editor.resize.nw': 'Resize top-left corner',
    'editor.style.fill': 'Fill color',
    'editor.style.text': 'Text color',
    'editor.style.border': 'Border color',
    'editor.style.width': 'Border width',

    'errors.requestFailed': 'Request failed.',
    'errors.badRequest': 'The request is invalid.',
    'errors.invalidCredentials': 'Email or password is incorrect.',
    'errors.loginRateLimited': 'Too many login attempts. Try again later.',
    'errors.sessionCreateFailed': 'Could not create a session.',
    'errors.validationFailed': 'Please check the submitted information.',
    'errors.invalidCurrentPassword': 'Current password is incorrect.',
    'errors.uploadTooLarge': 'The uploaded file exceeds the configured size limit.',
    'errors.invalidMultipart': 'The upload request is invalid.',
    'errors.invalidProjectID': 'The project identifier is invalid.',
    'errors.uploadFailed': 'Could not upload the file.',
    'errors.unsupportedImage': 'This image format is not supported.',
    'errors.notFound': 'Resource not found.',
    'errors.assetReadFailed': 'Could not read the image.',
    'errors.originNotAllowed': 'This request origin is not allowed.',
    'errors.requestTooLarge': 'The request is too large.',
    'errors.unsupportedMediaType': 'The request format is not supported.',
    'errors.invalidJSON': 'The request data is invalid.',
    'errors.authenticationRequired': 'Please sign in to continue.',
    'errors.passwordChangeRequired': 'Change your password before continuing.',
    'errors.systemAdminRequired': 'System administrator access is required.',
    'errors.notReady': 'The service is not ready yet.',
    'errors.serviceUnavailable': 'The service is temporarily unavailable.',
    'errors.conflict': 'The resource conflicts with its current state.',
    'errors.forbidden': 'This operation is not allowed.',
    'errors.projectAccessDenied': 'You do not have access to this project.',
    'errors.projectAdminRequired': 'Project administrator access is required.',
    'errors.editorRequired': 'Project editor access is required.',
    'errors.assetAccessDenied': 'You do not have access to this image.',
    'errors.boardAccessDenied': 'You do not have access to this board.',
    'errors.lastOwnerRequired': 'A project must retain at least one owner.',
    'errors.lastSystemAdminRequired': 'At least one system administrator is required.',
    'errors.assetReferenceIndexStale': 'Image references are still being synchronized. Try again shortly.',
    'errors.assetReferenceConflict': 'Conflicting image references were reported.',
    'errors.assetInUse': 'This image is still used by a board.',
    'errors.invalidAssetReference': 'All images must belong to this project.',
    'errors.ownerRequired': 'Only an owner may modify owner membership.',
    'errors.shuttingDown': 'The service is shutting down.',
    'errors.methodNotAllowed': 'This operation is not supported.',
    'errors.boardUnavailable': 'This board was deleted or is no longer available.',
    'errors.boardAccessRevoked': 'Your access to this board was revoked.',
    'errors.collaboration': 'Collaboration error.',
    'errors.collaborationAuthorization': 'Could not verify collaboration access.',
    'errors.collaborationSync': 'Could not synchronize the board.',
    'errors.collaborationRestored': 'The server document was restored to an earlier sequence; authoritative state is being reloaded.',
    'errors.collaborationIncomplete': 'Collaboration sync ended before all ordered updates were applied.',
    'errors.awarenessOwner': 'Ignored a presence identity owned by another connection.',
    'errors.awarenessMalformed': 'Ignored a malformed presence update.',
    'errors.imageReferences': 'Could not synchronize image references.',
    'errors.unsupportedProtocol': 'Unsupported collaboration protocol {actual}; expected v{expected}.',
    'errors.invalidCheckpoint': 'Stored board checkpoint is invalid: {details}',
    'errors.invalidDocumentUpdate': 'Ignored invalid document update at sequence {sequence}: {details}'
  },
  zh_cn: {
    'app.loading': '加载中',
    'common.save': '保存',
    'common.cancel': '取消',
    'common.delete': '删除',
    'common.retry': '重试',
    'nav.projects': '项目',
    'nav.admin': '系统管理',
    'nav.signOut': '退出登录',
    'locale.label': '语言',
    'locale.en': 'English',
    'locale.zh_cn': '简体中文',

    'login.subtitle': '面向项目的在线协作白板，支持结构化物块、资源和实时编辑。',
    'login.email': '邮箱',
    'login.password': '密码',
    'login.signIn': '登录',
    'login.failed': '登录失败',

    'password.title': '修改密码',
    'password.subtitle': '继续使用前，请设置仅自己知晓的密码。',
    'password.current': '当前密码',
    'password.new': '新密码',
    'password.confirm': '确认密码',
    'password.mismatch': '两次输入的密码不一致。',
    'password.save': '保存密码',

    'admin.title': '系统用户',
    'admin.subtitle': '新用户首次登录时必须更换一次性密码。',
    'admin.tempPassword': '新建用户的临时密码为 {password}。',
    'admin.emailPlaceholder': '邮箱',
    'admin.namePlaceholder': '姓名',
    'admin.passwordPlaceholder': '一次性密码',
    'admin.add': '添加',
    'admin.createFailed': '创建用户失败',
    'admin.empty': '暂无用户。',
    'admin.systemRoleFor': '{email} 的系统角色',
    'admin.oneTimePasswordFor': '{email} 的一次性密码',
    'admin.resetPassword': '重置密码',
    'admin.role.user': '普通用户',
    'admin.role.system_admin': '系统管理员',

    'projects.title': '项目',
    'projects.newProject': '新项目',
    'projects.projectName': '项目名称',
    'projects.descriptionOptional': '描述（可选）',
    'projects.create': '创建',
    'projects.empty': '创建你的第一个项目。',
    'projects.boardsFallback': '白板',
    'projects.boardsDescription': '创建并打开项目白板。',
    'projects.deleteProjectConfirm': '确定删除“{name}”及其全部白板和上传文件吗？',
    'projects.boardName': '白板名称',
    'projects.board': '白板',
    'projects.noBoards': '暂无白板。',
    'projects.members': '成员',
    'projects.selectUser': '选择用户',
    'projects.userID': '用户 ID',
    'projects.add': '添加',
    'projects.noMembers': '暂无成员。',
    'projects.editProject': '编辑项目',
    'projects.description': '描述',
    'projects.updated': '更新于 {time}',
    'projects.rename': '重命名',
    'projects.deleteBoardConfirm': '确定删除“{name}”吗？',
    'projects.removeMember': '移除成员',
    'projects.removeMemberConfirm': '确定移除该成员吗？',
    'projects.createProjectFailed': '创建项目失败',
    'projects.updatedAt': 'v{version} · {time}',
    'role.owner': '所有者',
    'role.admin': '管理员',
    'role.editor': '编辑者',
    'role.viewer': '查看者',

    'editor.loadingBoard': '正在加载白板…',
    'editor.unableToOpen': '无法打开白板',
    'editor.boardNotFound': '未找到白板',
    'editor.backToProjects': '返回项目',
    'editor.projectFallback': '项目',
    'editor.toolbarLabel': '白板工具',
    'editor.saved': '已保存 {time}',
    'editor.saving': '保存中…',
    'editor.connection.savingOne': '正在保存 {count} 项更新',
    'editor.connection.savingMany': '正在保存 {count} 项更新',
    'editor.connection.synced': '已同步 · 序号 {sequence}',
    'editor.connection.live': '在线',
    'editor.connection.connecting': '连接中',
    'editor.connection.syncing': '同步中',
    'editor.connection.offline': '离线',
    'editor.back': '返回',
    'editor.select': '选择（V）',
    'editor.pan': '平移',
    'editor.textBox': '文本框',
    'editor.textTool': '文本',
    'editor.uploadImage': '上传图片',
    'editor.undo': '撤销',
    'editor.redo': '重做',
    'editor.duplicate': '创建副本',
    'editor.bringToFront': '置于顶层',
    'editor.sendToBack': '置于底层',
    'editor.moveForward': '上移一层',
    'editor.moveBackward': '下移一层',
    'editor.alignHorizontal': '水平居中对齐',
    'editor.alignVertical': '垂直居中对齐',
    'editor.distributeHorizontal': '水平等距分布',
    'editor.distributeVertical': '垂直等距分布',
    'editor.zoomOut': '缩小',
    'editor.zoomIn': '放大',
    'editor.fitAll': '缩放至全部内容',
    'editor.fitSelection': '缩放至选区',
    'editor.resetView': '重置视图',
    'editor.delete': '删除',
    'editor.dragBlock': '拖动物块',
    'editor.fill': '填充',
    'editor.text': '文字',
    'editor.border': '边框',
    'editor.width': '宽度',
    'editor.noFill': '无填充',
    'editor.aspectLock': '锁定比例',
    'editor.defaultText': '文本',
    'editor.upload.invalidType': '请选择 PNG、JPEG 或 GIF 图片。',
    'editor.upload.tooLarge': '图片超过 25 MB 上传限制。',
    'editor.upload.decodeFailed': '无法解析该图片。',
    'editor.upload.failed': '上传失败。',
    'editor.upload.progress': '正在上传 {file}：{progress}%',
    'editor.upload.cancel': '取消',
    'editor.upload.retry': '重试',
    'editor.canvasStats': '共 {total} 个物块 · 已渲染 {rendered} 个',
    'editor.moveTextBlock': '移动文本物块',
    'editor.resize.n': '调整上边缘',
    'editor.resize.ne': '调整右上角',
    'editor.resize.e': '调整右边缘',
    'editor.resize.se': '调整右下角',
    'editor.resize.s': '调整下边缘',
    'editor.resize.sw': '调整左下角',
    'editor.resize.w': '调整左边缘',
    'editor.resize.nw': '调整左上角',
    'editor.style.fill': '填充颜色',
    'editor.style.text': '文字颜色',
    'editor.style.border': '边框颜色',
    'editor.style.width': '边框宽度',

    'errors.requestFailed': '请求失败。',
    'errors.badRequest': '请求无效。',
    'errors.invalidCredentials': '邮箱或密码错误。',
    'errors.loginRateLimited': '登录尝试次数过多，请稍后重试。',
    'errors.sessionCreateFailed': '无法创建登录会话。',
    'errors.validationFailed': '请检查提交的信息。',
    'errors.invalidCurrentPassword': '当前密码错误。',
    'errors.uploadTooLarge': '上传文件超过服务器大小限制。',
    'errors.invalidMultipart': '上传请求格式无效。',
    'errors.invalidProjectID': '项目标识无效。',
    'errors.uploadFailed': '无法上传文件。',
    'errors.unsupportedImage': '不支持这种图片格式。',
    'errors.notFound': '未找到资源。',
    'errors.assetReadFailed': '无法读取图片。',
    'errors.originNotAllowed': '不允许从当前来源发起请求。',
    'errors.requestTooLarge': '请求内容过大。',
    'errors.unsupportedMediaType': '不支持该请求格式。',
    'errors.invalidJSON': '请求数据无效。',
    'errors.authenticationRequired': '请先登录。',
    'errors.passwordChangeRequired': '请先修改密码。',
    'errors.systemAdminRequired': '需要系统管理员权限。',
    'errors.notReady': '服务尚未就绪。',
    'errors.serviceUnavailable': '服务暂时不可用。',
    'errors.conflict': '资源与当前状态冲突。',
    'errors.forbidden': '不允许执行此操作。',
    'errors.projectAccessDenied': '你无权访问该项目。',
    'errors.projectAdminRequired': '需要项目管理员权限。',
    'errors.editorRequired': '需要项目编辑权限。',
    'errors.assetAccessDenied': '你无权访问该图片。',
    'errors.boardAccessDenied': '你无权访问该白板。',
    'errors.lastOwnerRequired': '项目必须至少保留一位所有者。',
    'errors.lastSystemAdminRequired': '系统必须至少保留一位系统管理员。',
    'errors.assetReferenceIndexStale': '图片引用仍在同步，请稍后重试。',
    'errors.assetReferenceConflict': '检测到冲突的图片引用。',
    'errors.assetInUse': '该图片仍被白板使用。',
    'errors.invalidAssetReference': '所有图片都必须属于当前项目。',
    'errors.ownerRequired': '只有所有者可以修改所有者成员关系。',
    'errors.shuttingDown': '服务正在关闭。',
    'errors.methodNotAllowed': '不支持此操作。',
    'errors.boardUnavailable': '该白板已被删除或不再可用。',
    'errors.boardAccessRevoked': '你已失去该白板的访问权限。',
    'errors.collaboration': '协作发生错误。',
    'errors.collaborationAuthorization': '无法验证白板协作权限。',
    'errors.collaborationSync': '无法同步白板。',
    'errors.collaborationRestored': '服务器文档已恢复到较早版本，正在重新加载权威状态。',
    'errors.collaborationIncomplete': '协作同步结束时仍有有序更新尚未应用。',
    'errors.awarenessOwner': '已忽略属于其他连接的在线状态标识。',
    'errors.awarenessMalformed': '已忽略格式错误的在线状态更新。',
    'errors.imageReferences': '无法同步图片引用。',
    'errors.unsupportedProtocol': '不支持协作协议 {actual}；需要 v{expected}。',
    'errors.invalidCheckpoint': '白板检查点无效：{details}',
    'errors.invalidDocumentUpdate': '已忽略序号 {sequence} 的无效文档更新：{details}'
  }
} as const;

type MessageKey = keyof typeof messages.en;
type Params = Record<string, string | number>;

const apiErrorMessages: Record<string, MessageKey> = {
  bad_request: 'errors.badRequest',
  unauthorized: 'errors.authenticationRequired',
  request_failed: 'errors.requestFailed',
  invalid_credentials: 'errors.invalidCredentials',
  login_rate_limited: 'errors.loginRateLimited',
  session_create_failed: 'errors.sessionCreateFailed',
  validation_failed: 'errors.validationFailed',
  invalid_current_password: 'errors.invalidCurrentPassword',
  upload_too_large: 'errors.uploadTooLarge',
  invalid_multipart: 'errors.invalidMultipart',
  invalid_project_id: 'errors.invalidProjectID',
  upload_failed: 'errors.uploadFailed',
  unsupported_image: 'errors.unsupportedImage',
  not_found: 'errors.notFound',
  asset_read_failed: 'errors.assetReadFailed',
  origin_not_allowed: 'errors.originNotAllowed',
  request_too_large: 'errors.requestTooLarge',
  unsupported_media_type: 'errors.unsupportedMediaType',
  invalid_json: 'errors.invalidJSON',
  authentication_required: 'errors.authenticationRequired',
  password_change_required: 'errors.passwordChangeRequired',
  system_admin_required: 'errors.systemAdminRequired',
  not_ready: 'errors.notReady',
  service_unavailable: 'errors.serviceUnavailable',
  conflict: 'errors.conflict',
  forbidden: 'errors.forbidden',
  project_access_denied: 'errors.projectAccessDenied',
  project_admin_required: 'errors.projectAdminRequired',
  editor_required: 'errors.editorRequired',
  asset_access_denied: 'errors.assetAccessDenied',
  board_access_denied: 'errors.boardAccessDenied',
  last_owner_required: 'errors.lastOwnerRequired',
  last_system_admin_required: 'errors.lastSystemAdminRequired',
  asset_reference_index_stale: 'errors.assetReferenceIndexStale',
  asset_reference_conflict: 'errors.assetReferenceConflict',
  asset_in_use: 'errors.assetInUse',
  invalid_asset_reference: 'errors.invalidAssetReference',
  owner_required: 'errors.ownerRequired',
  shutting_down: 'errors.shuttingDown',
  method_not_allowed: 'errors.methodNotAllowed'
};

const literalErrorMessages: Record<string, MessageKey> = {
  'Upload failed': 'errors.uploadFailed',
  'This board was deleted or is no longer available': 'errors.boardUnavailable',
  'Your access to this board was revoked': 'errors.boardAccessRevoked',
  'board access was revoked': 'errors.boardAccessRevoked',
  'Collaboration error': 'errors.collaboration',
  'could not verify board permission': 'errors.collaborationAuthorization',
  'could not verify collaboration access': 'errors.collaborationAuthorization',
  'could not synchronize board': 'errors.collaborationSync',
  'The server document was restored to an earlier sequence; reloading authoritative state': 'errors.collaborationRestored',
  'Collaboration sync ended before all ordered updates were applied': 'errors.collaborationIncomplete',
  'Ignored an awareness identity owned by another connection': 'errors.awarenessOwner',
  'Ignored a malformed awareness update': 'errors.awarenessMalformed',
  'Could not synchronize image references': 'errors.imageReferences',
  'asset references must exist in the board project': 'errors.invalidAssetReference',
  'viewer cannot update the document': 'errors.forbidden'
};

interface I18nContextValue {
  locale: Locale;
  setLocale: (locale: Locale) => void;
  t: (key: MessageKey, params?: Params) => string;
  errorMessage: (error: unknown, fallback?: MessageKey) => string;
  formatDateTime: (value: string | Date) => string;
  formatTime: (value: string | Date) => string;
}

const I18nContext = createContext<I18nContextValue | null>(null);

export function I18nProvider({ children }: { children: ReactNode }) {
  const [locale, setLocaleState] = useState<Locale>(() => initialLocale());

  useEffect(() => {
    document.documentElement.lang = locale === 'zh_cn' ? 'zh-CN' : 'en';
    try {
      localStorage.setItem('dw_locale', locale);
    } catch {
      // Language selection still works for the current page lifecycle.
    }
  }, [locale]);

  const value = useMemo<I18nContextValue>(() => {
    function t(key: MessageKey, params: Params = {}) {
      const template: string = messages[locale][key] ?? messages.en[key] ?? key;
      return Object.entries(params).reduce<string>((text, [name, replacement]) => text.split(`{${name}}`).join(String(replacement)), template);
    }

    function errorMessage(error: unknown, fallback?: MessageKey) {
      if (error instanceof APIError && error.code) {
        const key = apiErrorMessages[error.code];
        if (key) return t(key);
      }
      const message = error instanceof Error ? error.message : typeof error === 'string' ? error : '';
      const literalKey = literalErrorMessages[message];
      if (literalKey) return t(literalKey);

      let match = /^Unsupported collaboration protocol (.+); expected v(.+)$/.exec(message);
      if (match) return t('errors.unsupportedProtocol', { actual: match[1], expected: match[2] });
      match = /^Stored board checkpoint is invalid: (.+)$/.exec(message);
      if (match) return t('errors.invalidCheckpoint', { details: match[1] });
      match = /^Ignored invalid document update at sequence (.+?): (.+)$/.exec(message);
      if (match) return t('errors.invalidDocumentUpdate', { sequence: match[1], details: match[2] });

      if (fallback) return t(fallback);
      return message || t('errors.requestFailed');
    }

    const dateLocale = locale === 'zh_cn' ? 'zh-CN' : 'en';
    return {
      locale,
      setLocale: setLocaleState,
      t,
      errorMessage,
      formatDateTime: (value) => new Date(value).toLocaleString(dateLocale),
      formatTime: (value) => new Date(value).toLocaleTimeString(dateLocale, { hour: '2-digit', minute: '2-digit' })
    };
  }, [locale]);

  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>;
}

export function useI18n() {
  const value = useContext(I18nContext);
  if (!value) throw new Error('useI18n must be used inside I18nProvider');
  return value;
}

export function LocaleSelect() {
  const { locale, setLocale, t } = useI18n();
  return (
    <label className="locale-select">
      <span>{t('locale.label')}</span>
      <select value={locale} onChange={(event) => setLocale(event.target.value as Locale)}>
        <option value="en">{t('locale.en')}</option>
        <option value="zh_cn">{t('locale.zh_cn')}</option>
      </select>
    </label>
  );
}

function initialLocale(): Locale {
  let saved: string | null = null;
  try {
    saved = localStorage.getItem('dw_locale');
  } catch {
    // Storage can be unavailable in hardened or private browser contexts.
  }
  if (saved === 'en' || saved === 'zh_cn') return saved;
  return navigator.language.toLowerCase().startsWith('zh') ? 'zh_cn' : 'en';
}
