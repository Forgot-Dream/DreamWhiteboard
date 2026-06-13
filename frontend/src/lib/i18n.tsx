import { createContext, useContext, useEffect, useMemo, useState, type ReactNode } from 'react';

export type Locale = 'en' | 'zh_cn';

const messages = {
  en: {
    'app.loading': 'Loading',
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

    'admin.title': 'System Users',
    'admin.tempPassword': 'Created users receive the temporary password {password}.',
    'admin.emailPlaceholder': 'email',
    'admin.namePlaceholder': 'name',
    'admin.add': 'Add',
    'admin.createFailed': 'Create user failed',
    'admin.role.user': 'user',
    'admin.role.system_admin': 'system_admin',

    'projects.title': 'Projects',
    'projects.newProject': 'New project',
    'projects.create': 'Create',
    'projects.boardsFallback': 'Boards',
    'projects.boardsDescription': 'Create and open project whiteboards.',
    'projects.boardName': 'Board name',
    'projects.board': 'Board',
    'projects.members': 'Members',
    'projects.selectUser': 'Select user',
    'projects.add': 'Add',
    'projects.createProjectFailed': 'Create project failed',
    'projects.updatedAt': 'v{version} · {time}',
    'role.owner': 'owner',
    'role.admin': 'admin',
    'role.editor': 'editor',
    'role.viewer': 'viewer',

    'editor.saved': 'Saved {time}',
    'editor.saving': 'Saving...',
    'editor.connection.live': 'live',
    'editor.connection.connecting': 'connecting',
    'editor.connection.offline': 'offline',
    'editor.back': 'Back',
    'editor.select': 'Select',
    'editor.textBox': 'Text box',
    'editor.uploadImage': 'Upload image',
    'editor.zoomOut': 'Zoom out',
    'editor.zoomIn': 'Zoom in',
    'editor.delete': 'Delete',
    'editor.dragBlock': 'Drag block',
    'editor.fill': 'Fill',
    'editor.text': 'Text',
    'editor.border': 'Border',
    'editor.width': 'Width',
    'editor.noFill': 'No fill',
    'editor.aspectLock': 'Lock ratio',
    'editor.defaultText': 'Text',
    'editor.style.fill': 'Fill color',
    'editor.style.text': 'Text color',
    'editor.style.border': 'Border color',
    'editor.style.width': 'Border width'
  },
  zh_cn: {
    'app.loading': '加载中',
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

    'admin.title': '系统用户',
    'admin.tempPassword': '新建用户的临时密码为 {password}。',
    'admin.emailPlaceholder': '邮箱',
    'admin.namePlaceholder': '姓名',
    'admin.add': '添加',
    'admin.createFailed': '创建用户失败',
    'admin.role.user': '普通用户',
    'admin.role.system_admin': '系统管理员',

    'projects.title': '项目',
    'projects.newProject': '新项目',
    'projects.create': '创建',
    'projects.boardsFallback': '白板',
    'projects.boardsDescription': '创建并打开项目白板。',
    'projects.boardName': '白板名称',
    'projects.board': '白板',
    'projects.members': '成员',
    'projects.selectUser': '选择用户',
    'projects.add': '添加',
    'projects.createProjectFailed': '创建项目失败',
    'projects.updatedAt': 'v{version} · {time}',
    'role.owner': '所有者',
    'role.admin': '管理员',
    'role.editor': '编辑者',
    'role.viewer': '查看者',

    'editor.saved': '已保存 {time}',
    'editor.saving': '保存中...',
    'editor.connection.live': '在线',
    'editor.connection.connecting': '连接中',
    'editor.connection.offline': '离线',
    'editor.back': '返回',
    'editor.select': '选择',
    'editor.textBox': '文本框',
    'editor.uploadImage': '上传图片',
    'editor.zoomOut': '缩小',
    'editor.zoomIn': '放大',
    'editor.delete': '删除',
    'editor.dragBlock': '拖动物块',
    'editor.fill': '填充',
    'editor.text': '文字',
    'editor.border': '边框',
    'editor.width': '宽度',
    'editor.noFill': '无填充',
    'editor.aspectLock': '锁定比例',
    'editor.defaultText': '文本',
    'editor.style.fill': '填充颜色',
    'editor.style.text': '文字颜色',
    'editor.style.border': '边框颜色',
    'editor.style.width': '边框宽度'
  }
} as const;

type MessageKey = keyof typeof messages.en;
type Params = Record<string, string | number>;

interface I18nContextValue {
  locale: Locale;
  setLocale: (locale: Locale) => void;
  t: (key: MessageKey, params?: Params) => string;
  formatDateTime: (value: string | Date) => string;
  formatTime: (value: string | Date) => string;
}

const I18nContext = createContext<I18nContextValue | null>(null);

export function I18nProvider({ children }: { children: ReactNode }) {
  const [locale, setLocaleState] = useState<Locale>(() => initialLocale());

  useEffect(() => {
    document.documentElement.lang = locale === 'zh_cn' ? 'zh-CN' : 'en';
    localStorage.setItem('dw_locale', locale);
  }, [locale]);

  const value = useMemo<I18nContextValue>(() => {
    function t(key: MessageKey, params: Params = {}) {
      const template: string = messages[locale][key] ?? messages.en[key] ?? key;
      return Object.entries(params).reduce<string>((text, [name, replacement]) => text.split(`{${name}}`).join(String(replacement)), template);
    }

    const dateLocale = locale === 'zh_cn' ? 'zh-CN' : 'en';
    return {
      locale,
      setLocale: setLocaleState,
      t,
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
  const saved = localStorage.getItem('dw_locale');
  if (saved === 'en' || saved === 'zh_cn') return saved;
  return navigator.language.toLowerCase().startsWith('zh') ? 'zh_cn' : 'en';
}
