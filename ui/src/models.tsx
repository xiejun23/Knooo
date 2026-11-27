import { useEffect, useMemo, useRef, useState, type FormEvent } from 'react';
import {
 ArrowDown, ArrowLeft, Check, ChevronDown, ChevronRight, CirclePlus, Cpu, Database,
 Download, Eye, EyeOff, Link2, Plug, RefreshCw, Save, Search, Sparkles, Trash2, Upload, X,
} from 'lucide-react';
import { EagleMark } from './eagle';
import { dateValue } from './date-range';
import {
 analyzeQuickInput,
 buildDirectoryGroups,
 displayEndpoint,
 emptyProfile,
 matchesConfiguredChoice,
 protocolLabels,
 providerCategoryLabel,
 providerStatus,
 reasoningBadgeLabel,
 reasoningOptions,
 resolveIcon,
 resolveProtocol,
 serviceDisplayName,
 type APIProtocol,
 type Connections,
 type Profile,
} from './providers';
import openai from './assets/models/openai.svg?no-inline';
import claude from './assets/models/claude.svg?no-inline';
import gemini from './assets/models/gemini.svg?no-inline';
import deepseek from './assets/models/deepseek.svg?no-inline';
import qwen from './assets/models/qwen.svg?no-inline';
import kimi from './assets/models/kimi.svg?no-inline';
import minimax from './assets/models/minimax.svg?no-inline';
import zhipu from './assets/models/zhipu.svg?no-inline';
import mistral from './assets/models/mistral.svg?no-inline';
import openrouter from './assets/models/openrouter.svg?no-inline';
import grok from './assets/models/grok.svg?no-inline';
import magpie from './assets/models/magpie.svg?no-inline';
import ollama from './assets/models/ollama.svg?no-inline';
import lmstudio from './assets/models/lmstudio.svg?no-inline';
import ccswitch from './assets/models/ccswitch.png?no-inline';
import siliconflow from './assets/models/siliconflow.svg?no-inline';

export type { Connections, Preset, Profile } from './providers';

type DiscoveredConnection = { id: string; source: string; profile: Profile };
const discoverySourceNames: Readonly<Partial<Record<string, string>>> = {
 magpie: 'Magpie',
 'cc-switch': 'CC Switch',
 'claude-code': 'Claude Code',
};
type Discovery =
 | { kind: 'loading' }
 | { kind: 'ready'; items: DiscoveredConnection[] }
 | { kind: 'error'; message: string };
type Model = { id: string; display_name?: string; owned_by?: string; context_window?: number; max_output_tokens?: number };
type API = <T>(path: string, data?: unknown, signal?: AbortSignal) => Promise<T>;
type ReviewSettings =
 | { connectionId: string; maxAgeDays: number; settingsError?: never }
 | { connectionId: string; settingsError: string; maxAgeDays?: never };

const connectionIcons = [
 { id: 'generic', name: '通用', src: '' }, { id: 'openai', name: 'OpenAI', src: openai },
 { id: 'claude', name: 'Claude', src: claude }, { id: 'gemini', name: 'Gemini', src: gemini },
 { id: 'deepseek', name: 'DeepSeek', src: deepseek }, { id: 'qwen', name: 'Qwen', src: qwen },
 { id: 'kimi', name: 'Kimi', src: kimi }, { id: 'minimax', name: 'MiniMax', src: minimax },
 { id: 'zhipu', name: '智谱 GLM', src: zhipu }, { id: 'mistral', name: 'Mistral', src: mistral }, { id: 'openrouter', name: 'OpenRouter', src: openrouter },
 { id: 'grok', name: 'Grok', src: grok }, { id: 'magpie', name: 'Magpie', src: magpie },
 { id: 'ollama', name: 'Ollama', src: ollama }, { id: 'lmstudio', name: 'LM Studio', src: lmstudio },
 { id: 'ccswitch', name: 'CC Switch', src: ccswitch }, { id: 'siliconflow', name: '硅基流动', src: siliconflow },
];

function ConnectionIcon({ id, size = 18 }: { id: string; size?: number }) {
 const src = connectionIcons.find(icon => icon.id === id)?.src;
 return <span className="section-icon connection-icon" aria-hidden="true">
  {src ? <img src={src} alt="" width={size} height={size}/> : <Link2 size={Math.max(15, size - 2)}/>}
 </span>;
}

export function SettingsWorkspace({ connections, api, onChange, demo, downloadBackup, onRestore }: {
 connections: Connections; api: API; onChange: () => Promise<void>; demo: boolean;
 downloadBackup: () => Promise<Blob>; onRestore: () => void;
}) {
 const [reviewConnectionId, setReviewConnectionId] = useState<string | undefined>(demo ? '' : undefined);
 const [reviewMaxAgeDays, setReviewMaxAgeDays] = useState<number>(0);
 const [reviewLoadError, setReviewLoadError] = useState('');
 const [reviewWindowError, setReviewWindowError] = useState('');
 const [editing, setEditing] = useState(false);

 useEffect(() => {
  if (demo) return;
  const controller = new AbortController();
  api<ReviewSettings>('review-settings', undefined, controller.signal).then(({ connectionId, maxAgeDays, settingsError }) => {
   if (!controller.signal.aborted) {
    setReviewConnectionId(connectionId);
    setReviewMaxAgeDays(maxAgeDays ?? 0);
    setReviewLoadError('');
    setReviewWindowError(settingsError ?? '');
   }
  }).catch(err => {
   if (!controller.signal.aborted) setReviewLoadError(err instanceof Error ? err.message : String(err));
  });
  return () => controller.abort();
 }, [api, demo]);

 return <div className="settings-stack">
  <ModelSettings
   connections={connections}
   api={api}
   onChange={onChange}
   demo={demo}
   reviewConnectionId={reviewConnectionId ?? ''}
   onEditingChange={setEditing}
  />
  {!editing && <AutomaticReviewSettings
   profiles={connections.profiles}
   api={api}
   demo={demo}
   savedConnectionId={reviewConnectionId}
   savedMaxAgeDays={reviewMaxAgeDays}
   savedWindowError={reviewWindowError}
   loadError={reviewLoadError}
   onSavedChange={(id, days) => { setReviewConnectionId(id); setReviewMaxAgeDays(days); setReviewWindowError(''); }}
  />}
  {!editing && !demo && <DesktopStartupSettings api={api}/>}
  {!editing && !demo && <WorkspaceBackup api={api} download={downloadBackup} onRestore={onRestore}/>}
 </div>;
}

function WorkspaceBackup({ api, download, onRestore }: {
 api: API; download: () => Promise<Blob>; onRestore: () => void;
}) {
 const fileInput = useRef<HTMLInputElement>(null);
 const [pending, setPending] = useState<'export' | 'import'>();
 const [feedback, setFeedback] = useState<{ message: string; error?: boolean }>();

 async function exportBackup() {
  setPending('export');
  setFeedback(undefined);
  try {
   const url = URL.createObjectURL(await download());
   const link = document.createElement('a');
   link.href = url;
   link.download = `Knooo-backup-${dateValue(new Date())}.json`;
   link.click();
   setTimeout(() => URL.revokeObjectURL(url), 1000);
   setFeedback({ message: '已生成备份文件。' });
  } catch (err) {
   setFeedback({ message: err instanceof Error ? err.message : String(err), error: true });
  } finally {
   setPending(undefined);
  }
 }

 async function importBackup(file: File) {
  setPending('import');
  setFeedback(undefined);
  try {
   const result = await api<{ added: number; skipped: number }>('backup', file);
   setFeedback({ message: `导入完成，新增 ${result.added} 项，跳过 ${result.skipped} 项。` });
   if (result.added > 0) onRestore();
  } catch (err) {
   setFeedback({ message: err instanceof Error ? err.message : String(err), error: true });
  } finally {
   setPending(undefined);
  }
 }

 return <section className="settings-box workspace-backup">
  <h2>工作区备份</h2>
  <p>备份主题、知识条目、会话归档状态、工作回顾和报告。模型连接和 Agent 原始会话需另行迁移。</p>
  <p>导入会补入缺失条目，已有条目会跳过。</p>
  <div className="settings-actions">
   <button type="button" className="button secondary" disabled={!!pending} onClick={() => void exportBackup()}>
    <Download size={15}/>{pending === 'export' ? '正在导出…' : '导出备份'}
   </button>
   <button type="button" className="button secondary" disabled={!!pending} onClick={() => fileInput.current?.click()}>
    <Upload size={15}/>{pending === 'import' ? '正在导入…' : '导入备份'}
   </button>
   <input ref={fileInput} type="file" accept=".json,application/json" hidden onChange={event => {
    const file = event.currentTarget.files?.[0];
    event.currentTarget.value = '';
    if (file) void importBackup(file);
   }}/>
  </div>
  {feedback && <p className={feedback.error ? 'inline-error' : 'inline-note'} role={feedback.error ? 'alert' : 'status'}>{feedback.message}</p>}
 </section>;
}

type DesktopStartupState = 'unavailable' | 'off' | 'on' | 'blocked';

function readDesktopStartupState(response: unknown): DesktopStartupState {
 if (response !== null && typeof response === 'object' && 'state' in response) {
  const { state } = response;
  if (state === 'unavailable' || state === 'off' || state === 'on' || state === 'blocked') return state;
 }
 throw new Error('无法读取登录启动设置。');
}

function DesktopStartupSettings({ api }: { api: API }) {
 const [state, setState] = useState<DesktopStartupState>();
 const [error, setError] = useState('');
 const [saving, setSaving] = useState(false);

 useEffect(() => {
  const controller = new AbortController();
  api<unknown>('desktop-startup', undefined, controller.signal).then(readDesktopStartupState).then(value => {
   if (!controller.signal.aborted) setState(value);
  }).catch(err => {
   if (!controller.signal.aborted) setError(err instanceof Error ? err.message : String(err));
  });
  return () => controller.abort();
 }, [api]);

 if (state === 'unavailable' || (state === undefined && !error)) return null;
 const enabled = state === 'on' || state === 'blocked';

 async function toggle() {
  setSaving(true);
  setError('');
  try {
   const response = await api<unknown>('desktop-startup', { enabled: !enabled });
   setState(readDesktopStartupState(response));
  } catch (err) {
   setError(err instanceof Error ? err.message : String(err));
  } finally {
   setSaving(false);
  }
 }

 return <section className="settings-box desktop-startup">
  <div className="desktop-startup-row">
   <div>
    <h2 id="desktop-startup-title">登录 Windows 时启动 Knooo</h2>
    <p id="desktop-startup-description">登录后在系统托盘运行</p>
   </div>
   {state !== undefined && <button
    type="button"
    role="switch"
    aria-checked={enabled}
    aria-labelledby="desktop-startup-title"
    aria-describedby={state === 'blocked' ? 'desktop-startup-description desktop-startup-blocked' : 'desktop-startup-description'}
    className="provider-switch"
    disabled={saving}
    onClick={() => void toggle()}
   />}
  </div>
  {state === 'blocked' && <p id="desktop-startup-blocked" role="status">Windows 已禁用此启动项，可在系统的「启动应用」设置中启用。</p>}
  {error && <p className="inline-error" role="alert">{error}</p>}
 </section>;
}

const reviewScopeOptions = [
 { days: 1, label: '今天' },
 { days: 3, label: '最近 3 天' },
 { days: 7, label: '最近 7 天' },
 { days: 30, label: '最近 30 天（推荐）' },
 { days: 90, label: '最近 90 天' },
 { days: 0, label: '全部历史' },
] as const;

export function AutomaticReviewSettings({
 profiles,
 api,
 demo,
 savedConnectionId,
 savedMaxAgeDays,
 savedWindowError,
 loadError,
 onSavedChange,
}: {
 profiles: Profile[];
 api: API;
 demo: boolean;
 savedConnectionId?: string;
 savedMaxAgeDays?: number;
 savedWindowError?: string;
 loadError?: string;
 onSavedChange?: (id: string, maxAgeDays: number) => void;
}) {
 const [internalSaved, setInternalSaved] = useState<string>();
 const [internalDays, setInternalDays] = useState<number>(0);
 const [internalWindowError, setInternalWindowError] = useState('');
 const [selected, setSelected] = useState('');
 const [maxAgeDays, setMaxAgeDays] = useState<number>(30);
 const [busy, setBusy] = useState(false);
 const [error, setError] = useState('');
 const [status, setStatus] = useState('');

 const controlled = savedConnectionId !== undefined || onSavedChange !== undefined;
 const saved = controlled ? savedConnectionId : internalSaved;
 const savedDays = controlled ? (savedMaxAgeDays ?? 0) : internalDays;
 const windowError = controlled ? savedWindowError : internalWindowError;

 useEffect(() => {
  if (demo || controlled) return;
  const controller = new AbortController();
  api<ReviewSettings>('review-settings', undefined, controller.signal).then(({ connectionId, maxAgeDays: days, settingsError }) => {
   if (!controller.signal.aborted) {
    setInternalSaved(connectionId);
    setSelected(connectionId);
    const resolvedDays = settingsError ? 30 : (days ?? (connectionId ? 0 : 30));
    setInternalDays(days ?? 0);
    setInternalWindowError(settingsError ?? '');
    setMaxAgeDays(resolvedDays);
   }
  }).catch(err => {
   if (!controller.signal.aborted) setError(err instanceof Error ? err.message : String(err));
  });
  return () => controller.abort();
 }, [api, demo, controlled]);

 useEffect(() => {
  if (controlled && savedConnectionId !== undefined) {
   setSelected(savedConnectionId);
   setMaxAgeDays(savedWindowError ? 30 : (savedConnectionId ? (savedMaxAgeDays ?? 0) : ((savedMaxAgeDays ?? 0) || 30)));
  }
 }, [controlled, savedConnectionId, savedMaxAgeDays, savedWindowError]);

 if (demo) return null;

 const enabledProfiles = profiles.filter(profile => !profile.disabled);
 const chosen = profiles.find(profile => profile.id === selected);
 const savedProfile = profiles.find(profile => profile.id === saved);
 const activeRunning = !windowError && !!saved && !!savedProfile && !savedProfile.disabled && savedProfile.ready && !!savedProfile.model;
 const dirty = !!windowError || selected !== saved || (!!selected && maxAgeDays !== savedDays);

 async function save(event: FormEvent) {
  event.preventDefault();
  setBusy(true);
  setError('');
  setStatus('');
  try {
   await api('review-settings', { connectionId: selected, maxAgeDays });
   if (onSavedChange) onSavedChange(selected, maxAgeDays);
   else { setInternalSaved(selected); setInternalDays(maxAgeDays); setInternalWindowError(''); }
   setStatus(selected ? '自动工作回顾设置已保存。' : '自动工作回顾已关闭。');
  } catch (err) {
   setError(err instanceof Error ? err.message : String(err));
  } finally {
   setBusy(false);
  }
 }

 const displayError = error || windowError || loadError || '';
 const unavailableHint = selected && chosen
  ? chosen.disabled
   ? '该提供商当前已停用，请先在上方启用或改选其他提供商。'
   : (!chosen.ready || !chosen.model)
    ? '请先在上方为该提供商补全默认模型 ID 和所需密钥。'
    : ''
  : '';

 return <form className="settings-box auto-review-card" onSubmit={save}>
  <div className="auto-review-head">
   <div>
    <h2>自动工作回顾</h2>
    <p>启用后，每条会话静默 2 小时才自动提炼。工作回顾页可查看倒计时，也可立即提炼。已保存且有效的回顾会复用，只处理新增消息。{selected && maxAgeDays === 1 && '「今天」也包含静默期在今天才结束的昨晚会话。'}</p>
   </div>
   <span className="provider-status" data-status={activeRunning ? 'ready' : 'disabled'}>
    <span className="provider-status-dot" aria-hidden="true"/>
    <span>{windowError ? '已暂停' : activeRunning ? '后台运行中' : '未启用'}</span>
   </span>
  </div>
  <div className="auto-review-row">
   <label>
    <span className="visually-hidden">默认算力提供商</span>
    <select value={selected} disabled={busy || saved === undefined} onChange={event => { setSelected(event.target.value); setStatus(''); }}>
     <option value="">关闭自动回顾</option>
     {selected && !chosen && <option value={selected}>原提供商已删除</option>}
     {selected && chosen?.disabled && <option value={selected}>{chosen.name}（已停用）</option>}
     {enabledProfiles.map(profile => <option key={profile.id} value={profile.id}>
      {profile.name} · {profile.model || '待设默认模型'}
     </option>)}
    </select>
   </label>
   {selected && <label className="auto-review-scope">
    <span className="visually-hidden">自动提炼范围</span>
    <select value={maxAgeDays} disabled={busy || saved === undefined} aria-label="自动提炼范围" onChange={event => { setMaxAgeDays(Number(event.target.value)); setStatus(''); }}>
     {reviewScopeOptions.map(option => <option key={option.days} value={option.days}>范围：{option.label}</option>)}
    </select>
   </label>}
   <button type="submit" className="button primary" disabled={busy || saved === undefined || !dirty || !!selected && (!chosen || chosen.disabled || !chosen.ready || !chosen.model)}>
    {busy ? <EagleMark flying/> : <Save size={15}/>} {selected ? (saved ? '保存回顾设置' : '启用自动回顾') : '关闭自动回顾'}
   </button>
  </div>
  {unavailableHint && <p className="provider-hint">{unavailableHint}</p>}
  {displayError && <p className="inline-error" role="alert">{displayError}</p>}
  {status && <p className="inline-note" role="status"><Check size={15}/>{status}</p>}
 </form>;
}

function Catalog({ models, value, onSelect, label, disabled = false }: {
 models: Model[]; value: string; onSelect: (id: string) => void; label: string; disabled?: boolean;
}) {
 const selected = models.find(item => item.id === value);
 const limits = [
  selected?.context_window ? `上下文窗口 ${selected.context_window.toLocaleString()} token` : '',
  selected?.max_output_tokens ? `最大输出 ${selected.max_output_tokens.toLocaleString()} token` : '',
 ].filter(Boolean);
 return <div className="model-options">
  <select aria-label={label} value={selected?.id ?? ''} onChange={e => onSelect(e.target.value)} disabled={disabled}>
   <option value="" disabled>选择目录中的模型</option>
   {models.map(model => <option key={model.id} value={model.id}>
    {model.display_name && model.display_name !== model.id ? `${model.display_name} · ` : ''}{model.id}
    {model.owned_by && !model.id.startsWith(`${model.owned_by}/`) ? ` · ${model.owned_by}` : ''}
   </option>)}
  </select>
  {limits.length > 0 && <small className="muted">{limits.join(' · ')}</small>}
 </div>;
}

function ModelCatalogList({ models, value, onSelect, disabled }: {
 models: Model[]; value: string; onSelect: (id: string) => void; disabled: boolean;
}) {
 const [query, setQuery] = useState('');
 const filtered = useMemo(() => {
  const q = query.trim().toLowerCase();
  if (!q) return models;
  return models.filter(item =>
   item.id.toLowerCase().includes(q) ||
   (item.display_name?.toLowerCase().includes(q) ?? false) ||
   (item.owned_by?.toLowerCase().includes(q) ?? false),
  );
 }, [models, query]);

 return <div className="provider-catalog-box">
  <div className="provider-catalog-head">
   <span>已读取 {models.length} 个模型</span>
   {models.length > 6 && <div className="provider-inline-search">
    <Search size={13} aria-hidden="true"/>
    <input
     value={query}
     onChange={e => setQuery(e.target.value)}
     placeholder="筛选模型 ID…"
     aria-label="筛选已读取的模型"
     disabled={disabled}
    />
    {query && <button type="button" className="provider-clear-btn" aria-label="清空模型筛选" onClick={() => setQuery('')}><X size={12}/></button>}
   </div>}
  </div>
  <div className="provider-catalog-list" role="listbox" aria-label="提供商模型目录">
   {filtered.length > 0 ? filtered.slice(0, 120).map(item => {
    const active = item.id === value;
    const limits = [
     item.context_window ? `${Math.round(item.context_window / 1000)}K 上下文` : '',
     item.max_output_tokens ? `${item.max_output_tokens.toLocaleString()} 输出` : '',
    ].filter(Boolean);
    return <button
     key={item.id}
     type="button"
     role="option"
     aria-selected={active}
     disabled={disabled}
     className="provider-catalog-item"
     onClick={() => onSelect(item.id)}
    >
     <span className="provider-catalog-name">
      <strong>{item.id}</strong>
      {item.display_name && item.display_name !== item.id && <small>{item.display_name}</small>}
     </span>
     <span className="provider-catalog-meta">
      {limits.length > 0 && <small>{limits.join(' · ')}</small>}
      {active && <Check size={14} aria-hidden="true"/>}
     </span>
    </button>;
   }) : <p className="provider-catalog-empty">没有匹配 “{query}” 的模型。</p>}
  </div>
 </div>;
}

function ProviderDirectory({
 connections,
 discoveredCount,
 onChoose,
 onOpenImport,
 onClose,
 showClose,
 title = '添加提供商',
}: {
 connections: Connections;
 discoveredCount: number;
 onChoose: (profile: Profile, apiKey?: string) => void;
 onOpenImport?: () => void;
 onClose?: () => void;
 showClose: boolean;
 title?: string;
}) {
 const [query, setQuery] = useState('');
 const inputRef = useRef<HTMLInputElement>(null);
 const groups = useMemo(() => buildDirectoryGroups(connections), [connections]);
 const quickAction = useMemo(() => analyzeQuickInput(query, connections), [query, connections]);

 const filteredGroups = useMemo(() => {
  const q = query.trim().toLowerCase();
  if (!q || quickAction) return groups;
  return groups
   .map(group => ({
    ...group,
    choices: group.choices.filter(choice => choice.keywords.toLowerCase().includes(q)),
   }))
   .filter(group => group.choices.length > 0);
 }, [groups, query, quickAction]);

 const customChoice = useMemo(() => {
  const compatible = connections.presets.find(p => p.id === 'compatible');
  return compatible ? emptyProfile({ ...compatible, name: '自定义中转站' }) : null;
 }, [connections.presets]);

 return <section className="provider-directory" aria-label={title}>
  <div className="provider-directory-bar">
   <h3>{title}</h3>
   <div className="provider-directory-tools">
    <div className="provider-quick-input">
     <Search size={14} aria-hidden="true"/>
     <input
      ref={inputRef}
      value={query}
      onChange={e => setQuery(e.target.value)}
      placeholder="查找提供商，或粘贴地址 / 密钥…"
      aria-label="查找提供商，或粘贴 API 地址或密钥"
     />
     {query && <button type="button" className="provider-clear-btn" aria-label="清空搜索" onClick={() => setQuery('')}><X size={13}/></button>}
    </div>
    {customChoice && <button type="button" className="button secondary provider-tool-btn" onClick={() => onChoose(customChoice, quickAction?.apiKey)}>
     <CirclePlus size={14}/>自定义提供商
    </button>}
    {onOpenImport && discoveredCount > 0 && <button type="button" className="button text-button" onClick={onOpenImport}>
     导入（{discoveredCount}）…
    </button>}
    {showClose && onClose && <button type="button" className="button text-button" onClick={onClose}>关闭</button>}
   </div>
  </div>

  {quickAction && <div className="provider-quick-banner">
   <Sparkles size={15} aria-hidden="true"/>
   <div>
    <strong>{quickAction.label}</strong>
    <small>{quickAction.detail}</small>
   </div>
   <button type="button" className="button primary" onClick={() => onChoose(quickAction.profile, quickAction.apiKey)}>
    立即配置
   </button>
  </div>}

  {filteredGroups.length > 0 ? <div className="provider-directory-groups">
   {filteredGroups.map(group => <div key={group.id} className="provider-dir-group">
    <div className="provider-dir-group-head">
     <h4>{group.name}</h4>
     <span>{group.hint}</span>
    </div>
    <div className="provider-dir-grid">
     {group.choices.map(choice => {
      const configured = matchesConfiguredChoice(choice.profile, connections.profiles);
      return <button
       key={choice.key}
       type="button"
       className="provider-dir-tile"
       onClick={() => onChoose(choice.profile, quickAction?.apiKey)}
      >
       <ConnectionIcon id={resolveIcon(choice.profile)} size={18}/>
       <span className="provider-dir-name">{choice.profile.name}</span>
       {configured && <span className="provider-configured-dot" title="已启用同类提供商" aria-label="已配置"/>}
      </button>;
     })}
    </div>
   </div>)}
  </div> : <p className="provider-empty-inline">没有匹配的内置提供商，可直接点击「自定义提供商」接入任意兼容接口。</p>}
 </section>;
}

export function ModelSettings({
 connections,
 api,
 onChange,
 demo,
 reviewConnectionId = '',
 onEditingChange,
}: {
 connections: Connections;
 api: API;
 onChange: () => Promise<void>;
 demo: boolean;
 reviewConnectionId?: string;
 onEditingChange?: (editing: boolean) => void;
}) {
 const [draft, setDraft] = useState<Profile>();
 const [switchingTemplate, setSwitchingTemplate] = useState(false);
 const [showDirectory, setShowDirectory] = useState(false);
 const [showImport, setShowImport] = useState(false);
 const [showDisabled, setShowDisabled] = useState(false);
 const [rosterQuery, setRosterQuery] = useState('');
 const [key, setKey] = useState('');
 const [showKey, setShowKey] = useState(false);
 const [clearKey, setClearKey] = useState(false);
 const [models, setModels] = useState<Model[]>([]);
 const [pending, setPending] = useState<'' | 'save' | 'models' | 'test' | 'remove' | 'import' | 'toggle'>('');
 const [togglingId, setTogglingId] = useState('');
 const [error, setError] = useState('');
 const [status, setStatus] = useState('');
 const [confirmDelete, setConfirmDelete] = useState(false);
 const [discovery, setDiscovery] = useState<Discovery>({ kind: 'loading' });
 const [scan, setScan] = useState(0);

 const savedProfiles = connections.profiles;

 useEffect(() => {
  onEditingChange?.(!!draft);
 }, [draft, onEditingChange]);

 useEffect(() => {
  if (demo) return;
  const controller = new AbortController();
  api<DiscoveredConnection[]>('connections/discovery', undefined, controller.signal).then(items => {
   if (!controller.signal.aborted) setDiscovery({ kind: 'ready', items });
  }).catch((err: unknown) => {
   if (!controller.signal.aborted) {
    setDiscovery({ kind: 'error', message: err instanceof Error ? err.message : String(err) });
   }
  });
  return () => controller.abort();
 }, [api, demo, scan, savedProfiles]);

 function clearFeedback() { setError(''); setStatus(''); }

 function refreshDiscovery() {
  setDiscovery({ kind: 'loading' });
  setScan(value => value + 1);
 }

 function openEditor(profile: Profile, initialKey = '') {
  setDraft(profile);
  setSwitchingTemplate(false);
  setShowDirectory(false);
  setShowImport(false);
  setKey(initialKey);
  setShowKey(false);
  setClearKey(false);
  setModels([]);
  setConfirmDelete(false);
  clearFeedback();
 }

 function closeEditor(keepStatus = false) {
  setDraft(undefined);
  setSwitchingTemplate(false);
  setKey('');
  setShowKey(false);
  setClearKey(false);
  setModels([]);
  setConfirmDelete(false);
  setError('');
  if (!keepStatus) setStatus('');
 }

 async function toggleProvider(profile: Profile) {
  if (demo || pending) return;
  setPending('toggle');
  setTogglingId(profile.id);
  clearFeedback();
  try {
   const nextDisabled = !profile.disabled;
   await api<Profile>('connections', { ...profile, disabled: nextDisabled });
   await onChange();
   setStatus(nextDisabled ? `已停用 ${profile.name}` : `已启用 ${profile.name}`);
   if (nextDisabled) setShowDisabled(true);
  } catch (err) {
   setError(err instanceof Error ? err.message : String(err));
  } finally {
   setPending('');
   setTogglingId('');
  }
 }

 async function importConnection(id: string) {
  setPending('import');
  clearFeedback();
  try {
   const profile = await api<Profile>('connections/discovery', { id });
   await onChange();
   setStatus(`已导入 ${profile.name}`);
  } catch (err) {
   setError(err instanceof Error ? err.message : String(err));
   refreshDiscovery();
  } finally {
   setPending('');
  }
 }

 async function importAllDiscovered(items: DiscoveredConnection[]) {
  if (!items.length) return;
  setPending('import');
  clearFeedback();
  let imported = 0;
  try {
   for (const item of items) {
    await api<Profile>('connections/discovery', { id: item.id });
    imported++;
   }
   await onChange();
   setShowImport(false);
   setStatus(`已导入 ${imported} 个本机配置`);
  } catch (err) {
   await onChange();
   setError(err instanceof Error ? err.message : String(err));
   refreshDiscovery();
  } finally {
   setPending('');
  }
 }

 async function save(event: FormEvent) {
  event.preventDefault();
  if (!draft) return;
  setPending('save');
  clearFeedback();
  try {
   const profile = await api<Profile>('connections', { ...draft, apiKey: key, clearKey });
   setDraft(profile);
   setKey('');
   setClearKey(false);
   await onChange();
   setStatus(`已保存 ${profile.name}`);
  } catch (err) {
   setError(err instanceof Error ? err.message : String(err));
  } finally {
   setPending('');
  }
 }

 async function check(test: boolean) {
  if (!draft) return;
  const checksModel = test && !!draft.model.trim();
  setPending(test ? 'test' : 'models');
  clearFeedback();
  try {
   const next = await api<Model[]>('models', { ...draft, apiKey: key, test });
   if (checksModel) {
    setStatus(`连通测试通过 · ${draft.model.trim()}`);
   } else {
    setModels(next);
    setStatus(next.length ? `模型目录可访问，已读取 ${next.length} 个模型` : '模型目录可访问。可手动填写模型 ID 后测试连接。');
   }
  } catch (err) {
   setError(err instanceof Error ? err.message : String(err));
  } finally {
   setPending('');
  }
 }

 async function remove() {
  if (!draft?.id) return;
  setPending('remove');
  clearFeedback();
  try {
   const name = draft.name;
   await api('connections', { id: draft.id, remove: true });
   await onChange();
   closeEditor(true);
   setStatus(`已移除 ${name}`);
  } catch (err) {
   setError(err instanceof Error ? err.message : String(err));
  } finally {
   setPending('');
  }
 }

 const discoveredItems = discovery.kind === 'ready' ? discovery.items : [];
 const activeProfiles = useMemo(() => savedProfiles.filter(profile => !profile.disabled), [savedProfiles]);
 const disabledProfiles = useMemo(() => savedProfiles.filter(profile => profile.disabled), [savedProfiles]);

 const matchesRoster = (profile: Profile) => {
  const q = rosterQuery.trim().toLowerCase();
  if (!q) return true;
  return profile.name.toLowerCase().includes(q) ||
   profile.baseUrl.toLowerCase().includes(q) ||
   profile.model.toLowerCase().includes(q) ||
   protocolLabels[resolveProtocol(profile)].toLowerCase().includes(q);
 };

 const visibleActive = useMemo(() => activeProfiles.filter(matchesRoster), [activeProfiles, rosterQuery]);
 const visibleDisabled = useMemo(() => disabledProfiles.filter(matchesRoster), [disabledProfiles, rosterQuery]);
 const readyCount = activeProfiles.filter(profile => providerStatus(profile).kind === 'ready').length;

 // Detail / Editor view
 if (draft) {
  const gateway = draft.preset === 'gateway';
  const serviceName = serviceDisplayName(draft, connections);
  const protocol = resolveProtocol(draft);
  const protocolOptions: APIProtocol[] = gateway ? ['chat', 'responses'] : ['chat', 'responses', 'anthropic', 'gemini'];
  const currentStatus = draft.id ? providerStatus(draft) : null;

  return <section className="provider-shell" aria-label={draft.id ? `配置 ${draft.name}` : '新建算力提供商'}>
   <div className="provider-breadcrumb">
    <button type="button" className="button secondary provider-back-btn" disabled={!!pending} onClick={() => closeEditor()}>
     <ArrowLeft size={15}/>算力提供商
    </button>
    <span className="provider-breadcrumb-sep" aria-hidden="true">/</span>
    <span className="provider-breadcrumb-current">{draft.id ? (draft.name || '编辑提供商') : `新建 · ${serviceName}`}</span>
    <div className="provider-breadcrumb-actions">
     {currentStatus && <span className="provider-status" data-status={currentStatus.kind}>
      <span className="provider-status-dot" aria-hidden="true"/>
      <span>{currentStatus.label}</span>
     </span>}
     <button type="button" className="button text-button" disabled={!!pending} onClick={() => setSwitchingTemplate(v => !v)}>
      {switchingTemplate ? '返回表单' : '更换服务模板'}
     </button>
    </div>
   </div>

   {demo && <p className="inline-note">演示工作区可预览配置表单，正式工作区支持保存、加密密钥和连通测试。</p>}

   {switchingTemplate ? <div className="settings-box provider-editor-card">
    <ProviderDirectory
     connections={connections}
     discoveredCount={0}
     showClose
     title="更换服务模板"
     onClose={() => setSwitchingTemplate(false)}
     onChoose={(next, pastedKey) => {
      setDraft({
       ...next,
       id: draft.id,
       disabled: draft.disabled,
       reasoningEffort: draft.reasoningEffort,
       inputBudget: draft.inputBudget,
       outputBudget: draft.outputBudget,
      });
      if (pastedKey) setKey(pastedKey);
      setSwitchingTemplate(false);
      setModels([]);
      clearFeedback();
     }}
    />
   </div> : <form className="settings-box provider-editor-card" onSubmit={save}>
    <fieldset disabled={!!pending}>
     <div className="provider-editor-hero">
      <ConnectionIcon id={resolveIcon(draft)} size={20}/>
      <div className="provider-editor-hero-copy">
       <h2>{draft.name.trim() || serviceName}</h2>
       <p>{serviceName} · {providerCategoryLabel(draft)}</p>
      </div>
      {draft.id && !demo && <label className="provider-editor-switch">
       <span>{draft.disabled ? '已停用' : '已启用'}</span>
       <button
        type="button"
        role="switch"
        aria-checked={!draft.disabled}
        aria-label={draft.disabled ? '启用提供商' : '停用提供商'}
        className="provider-switch"
        onClick={() => { setDraft({ ...draft, disabled: !draft.disabled }); clearFeedback(); }}
       />
      </label>}
     </div>

     <div className="provider-form-section">
      <h3>基础连接</h3>
      <div className="connection-fields">
       <label>提供商名称
        <input autoFocus required maxLength={60} value={draft.name} onChange={e => setDraft({ ...draft, name: e.target.value })}/>
       </label>
       <label>API 地址
        <input
         type="url"
         required
         maxLength={500}
         placeholder={gateway ? 'http://127.0.0.1:3425/v1' : 'https://api.example.com/v1'}
         value={draft.baseUrl}
         onChange={e => { setDraft({ ...draft, baseUrl: e.target.value, ready: false }); setModels([]); clearFeedback(); }}
        />
       </label>
      </div>

      <div className="provider-field-block">
       <span className="provider-field-label">API 协议</span>
       <div className="provider-protocol-bar" role="group" aria-label="API 协议">
        {protocolOptions.map(option => <button
         key={option}
         type="button"
         aria-pressed={protocol === option}
         onClick={() => {
          setDraft(gateway
           ? { ...draft, gatewayApi: option === 'responses' ? 'responses' : 'chat', ready: false }
           : { ...draft, apiProtocol: option, ready: false });
          setModels([]);
          clearFeedback();
         }}
        >{protocolLabels[option]}</button>)}
       </div>
      </div>

      <div className="provider-field-block">
       <div className="provider-field-label-row">
        <label htmlFor="provider-api-key">{gateway ? 'API 密钥（可选）' : 'API 密钥'}</label>
        {draft.hasKey && !clearKey && <span className="provider-key-badge">已加密保存在本机</span>}
       </div>
       <div className="provider-secret-field">
        <input
         id="provider-api-key"
         type={showKey ? 'text' : 'password'}
         autoComplete="off"
         maxLength={4096}
         placeholder={draft.hasKey ? '已保存，留空将沿用现有密钥' : gateway ? '本机网关未开启鉴权时可留空' : '输入服务商提供的 API 密钥'}
         value={key}
         disabled={clearKey}
         onChange={e => { setKey(e.target.value); setModels([]); clearFeedback(); }}
        />
        <button type="button" className="icon-button" aria-label={showKey ? '隐藏密钥' : '显示密钥'} onClick={() => setShowKey(v => !v)}>
         {showKey ? <EyeOff size={15}/> : <Eye size={15}/>}
        </button>
       </div>
       {draft.id && draft.hasKey && <label className="connection-clear-key">
        <input type="checkbox" checked={clearKey} onChange={e => { setClearKey(e.target.checked); setModels([]); clearFeedback(); }}/>
        保存时清除已存密钥
       </label>}
      </div>
     </div>

     <div className="provider-form-section">
      <h3>默认模型与连通性</h3>
      <div className="provider-model-row">
       <label>默认模型 ID
        <input
         value={draft.model}
         maxLength={240}
         placeholder="例如 deepseek-chat、gpt-4.1 或点击右侧读取"
         autoComplete="off"
         onChange={e => { setDraft({ ...draft, model: e.target.value }); clearFeedback(); }}
        />
       </label>
       <button type="button" className="button secondary" disabled={demo || clearKey} onClick={() => void check(false)}>
        {pending === 'models' ? <EagleMark flying/> : <ArrowDown size={15}/>} {pending === 'models' ? '读取中…' : '读取模型'}
       </button>
       <button type="button" className="button secondary" disabled={demo || clearKey} onClick={() => void check(true)}>
        {pending === 'test' ? <EagleMark flying/> : <Plug size={15}/>} {pending === 'test' ? '测试中…' : '测试连接'}
       </button>
      </div>

      {models.length > 0 && <ModelCatalogList
       models={models}
       value={draft.model}
       disabled={!!pending}
       onSelect={model => { setDraft({ ...draft, model }); clearFeedback(); }}
      />}

      <div className="provider-field-block">
       <span className="provider-field-label">思考等级（Reasoning Effort）</span>
       <div className="provider-protocol-bar" role="group" aria-label="模型思考等级">
        {reasoningOptions.map(option => <button
         key={option.id || 'default'}
         type="button"
         aria-pressed={(draft.reasoningEffort ?? '') === option.id}
         onClick={() => { setDraft({ ...draft, reasoningEffort: option.id }); clearFeedback(); }}
        >{option.label}</button>)}
       </div>
       <p className="provider-hint">
        控制推理模型的思考深度与 Token 开销。日常提炼选「关闭」或「低」更省 Token；选「默认」则不传参数，交由模型或网关决定。
       </p>
      </div>

      <p className="provider-hint">
       {draft.model.trim() ? '点击「测试连接」会向该模型发送一条极短的探测消息。' : '未填写模型 ID 时，「测试连接」将验证模型目录连通性。'}
       API 密钥仅加密保存在本机，重启后自动恢复。
      </p>
     </div>

     <details className="connection-advanced">
      <summary>外观图标</summary>
      <fieldset className="connection-icon-picker">
       <legend>品牌图标</legend>
       <div>{connectionIcons.map(icon => <label key={icon.id} title={icon.name}>
        <input type="radio" name="connection-icon" aria-label={icon.name} checked={resolveIcon(draft) === icon.id} onChange={() => setDraft({ ...draft, icon: icon.id })}/>
        <ConnectionIcon id={icon.id} size={17}/>
       </label>)}</div>
      </fieldset>
     </details>
    </fieldset>

    {error && <p className="inline-error provider-feedback" role="alert">{error}</p>}
    {status && <p className="inline-note provider-feedback" role="status"><Check size={15}/>{status}</p>}

    <div className="provider-editor-footer">
     <button type="submit" className="button primary" disabled={demo || !!pending}>
      {pending === 'save' ? <EagleMark flying/> : <Save size={15}/>} {pending === 'save' ? '正在保存…' : '保存配置'}
     </button>
     <button type="button" className="button secondary" disabled={!!pending} onClick={() => closeEditor()}>
      返回列表
     </button>
     {draft.id && <button type="button" className="button text-button delete-connection" disabled={!!pending} onClick={() => confirmDelete ? void remove() : setConfirmDelete(true)}>
      {pending === 'remove' ? <EagleMark flying/> : <Trash2 size={15}/>} {pending === 'remove' ? '正在移除…' : confirmDelete ? '再次点击确认移除' : '移除提供商'}
     </button>}
    </div>
   </form>}
  </section>;
 }

 // Master Roster View (Magpie-inspired)
 const directoryOpen = showDirectory || savedProfiles.length === 0;

 return <section className="provider-shell" aria-labelledby="providers-heading">
  <header className="provider-section-head">
   <div>
    <h2 id="providers-heading">算力提供商</h2>
    <p>接入本机网关、官方模型厂商或中转站，按需启用或停用，供提炼、回顾与工作报告调用。</p>
   </div>
   <div className="provider-section-stats">
    {savedProfiles.length > 0 && <span className="provider-summary-pill">
     {readyCount} 个就绪{disabledProfiles.length > 0 ? ` · ${disabledProfiles.length} 个已停用` : ''}
    </span>}
    <button
     type="button"
     className="button primary"
     disabled={!!pending || !connections.presets.length}
     onClick={() => { setShowDirectory(v => !v); setShowImport(false); clearFeedback(); }}
    >
     <CirclePlus size={15}/>添加提供商
    </button>
   </div>
  </header>

  {demo && <p className="inline-note">演示工作区可预览算力提供商配置；在正式工作区可保存加密密钥并实际调用模型。</p>}

  {savedProfiles.length > 0 && <div className="provider-toolbar">
   <div className="provider-search">
    <Search size={15} aria-hidden="true"/>
    <input
     value={rosterQuery}
     onChange={e => setRosterQuery(e.target.value)}
     placeholder="查找提供商、域名或默认模型…"
     aria-label="查找已保存的算力提供商"
    />
    {rosterQuery && <button type="button" className="provider-clear-btn" aria-label="清空查找" onClick={() => setRosterQuery('')}><X size={13}/></button>}
   </div>
  </div>}

  {visibleActive.length > 0 ? <div className="provider-roster" role="list" aria-label="已启用的算力提供商">
   {visibleActive.map(profile => {
    const state = providerStatus(profile);
    const isDefaultReview = reviewConnectionId !== '' && reviewConnectionId === profile.id;
    const isToggling = pending === 'toggle' && togglingId === profile.id;
    return <div key={profile.id} className="provider-row" role="listitem">
     <button type="button" className="provider-row-main" disabled={!!pending} onClick={() => openEditor(profile)}>
      <ConnectionIcon id={resolveIcon(profile)} size={19}/>
      <span className="provider-row-copy">
       <span className="provider-row-title">
        <strong>{profile.name}</strong>
        <span className="provider-tag">{providerCategoryLabel(profile)}</span>
        {reasoningBadgeLabel(profile.reasoningEffort) && <span className="provider-tag">{reasoningBadgeLabel(profile.reasoningEffort)}</span>}
        {isDefaultReview && <span className="provider-tag is-accent">默认回顾</span>}
       </span>
       <small className="provider-row-sub">
        <span>{displayEndpoint(profile.baseUrl)}</span>
        <span>·</span>
        <span>{profile.model || '未设置默认模型'}</span>
        <span>·</span>
        <span>{protocolLabels[resolveProtocol(profile)]}</span>
       </small>
      </span>
     </button>
     <div className="provider-row-right">
      <span className="provider-status" data-status={state.kind}>
       <span className="provider-status-dot" aria-hidden="true"/>
       <span>{state.label}</span>
      </span>
      <button
       type="button"
       role="switch"
       aria-checked="true"
       aria-label={`停用 ${profile.name}`}
       title={demo ? '演示模式不可修改' : `停用 ${profile.name}`}
       className="provider-switch"
       disabled={demo || !!pending}
       onClick={() => void toggleProvider(profile)}
      >
       {isToggling && <span className="visually-hidden">正在更新</span>}
      </button>
      <button type="button" className="icon-button provider-row-chevron" aria-label={`配置 ${profile.name}`} disabled={!!pending} onClick={() => openEditor(profile)}>
       <ChevronRight size={16}/>
      </button>
     </div>
    </div>;
   })}
  </div> : savedProfiles.length > 0 && rosterQuery && visibleDisabled.length === 0 ? <div className="empty provider-empty-card">
   <h3>没有匹配的提供商</h3>
   <p>试试更换关键词，或清空查找条件。</p>
   <button type="button" className="button secondary" onClick={() => setRosterQuery('')}>清空查找</button>
  </div> : savedProfiles.length === 0 ? <div className="empty provider-empty-card">
   <span className="empty-icon"><Cpu size={20}/></span>
   <h3>尚未接入算力提供商</h3>
   <p>从下方选择本机网关（如 Magpie、Ollama）或云端模型厂商，也可以直接粘贴地址与密钥快速配置。</p>
  </div> : null}

  {visibleDisabled.length > 0 && <div className="provider-disabled-group">
   <button type="button" className="provider-disabled-toggle" aria-expanded={showDisabled || !!rosterQuery} onClick={() => setShowDisabled(v => !v)}>
    {showDisabled || rosterQuery ? <ChevronDown size={15}/> : <ChevronRight size={15}/>}
    <span>已停用的提供商</span>
    <span className="provider-disabled-count">{visibleDisabled.length}</span>
   </button>
   {(showDisabled || !!rosterQuery) && <div className="provider-roster is-disabled" role="list" aria-label="已停用的算力提供商">
    {visibleDisabled.map(profile => <div key={profile.id} className="provider-row" role="listitem">
     <button type="button" className="provider-row-main" disabled={!!pending} onClick={() => openEditor(profile)}>
      <ConnectionIcon id={resolveIcon(profile)} size={19}/>
      <span className="provider-row-copy">
       <span className="provider-row-title">
        <strong>{profile.name}</strong>
        <span className="provider-tag">已停用</span>
        <span className="provider-tag">{providerCategoryLabel(profile)}</span>
       </span>
       <small className="provider-row-sub">
        <span>{displayEndpoint(profile.baseUrl)}</span>
        <span>·</span>
        <span>{profile.model || '未设置默认模型'}</span>
       </small>
      </span>
     </button>
     <div className="provider-row-right">
      <span className="provider-status" data-status="disabled">
       <span className="provider-status-dot" aria-hidden="true"/>
       <span>{profile.hasKey ? '密钥已保留' : '已停用'}</span>
      </span>
      <button
       type="button"
       role="switch"
       aria-checked="false"
       aria-label={`启用 ${profile.name}`}
       title={demo ? '演示模式不可修改' : `启用 ${profile.name}`}
       className="provider-switch"
       disabled={demo || !!pending}
       onClick={() => void toggleProvider(profile)}
      />
      <button type="button" className="icon-button provider-row-chevron" aria-label={`配置 ${profile.name}`} disabled={!!pending} onClick={() => openEditor(profile)}>
       <ChevronRight size={16}/>
      </button>
     </div>
    </div>)}
   </div>}
  </div>}

  <div className="provider-footer-bar">
   <button
    type="button"
    className="button text-button provider-footer-add"
    disabled={!!pending || !connections.presets.length}
    onClick={() => { setShowDirectory(v => !v); setShowImport(false); }}
   >
    <CirclePlus size={15}/>{directoryOpen && savedProfiles.length > 0 ? '收起提供商目录' : '添加提供商'}
   </button>

   {!demo && <div className="provider-footer-right">
    {discovery.kind === 'loading' && <span className="muted">正在扫描本机配置…</span>}
    {discovery.kind === 'error' && <button type="button" className="button text-button" onClick={refreshDiscovery}>
     本机扫描失败，点击重试
    </button>}
    {discovery.kind === 'ready' && discoveredItems.length > 0 && <button
     type="button"
     className="button secondary provider-import-trigger"
     aria-expanded={showImport}
     onClick={() => { setShowImport(v => !v); if (!showImport) setShowDirectory(false); }}
    >
     <Download size={14}/>导入本地配置（{discoveredItems.length}）
    </button>}
    {discovery.kind === 'ready' && discoveredItems.length === 0 && <button type="button" className="button text-button" disabled={!!pending} onClick={refreshDiscovery}>
     <RefreshCw size={13}/>扫描本机配置
    </button>}
   </div>}
  </div>

  {error && <p className="inline-error provider-feedback" role="alert">{error}</p>}
  {status && <p className="inline-note provider-feedback" role="status"><Check size={15}/>{status}</p>}

  {!demo && showImport && <section className="provider-import-panel" aria-labelledby="provider-import-title">
   <div className="provider-import-head">
    <div>
     <h3 id="provider-import-title">从本机工具导入</h3>
     <p>自动读取 Magpie、CC Switch 与 Claude Code 中可用的 API 配置，导入后独立加密保存。</p>
    </div>
    <div className="provider-import-actions">
     {discoveredItems.length > 1 && <button type="button" className="button primary" disabled={!!pending} onClick={() => void importAllDiscovered(discoveredItems)}>
      {pending === 'import' ? '正在导入…' : `全部导入（${discoveredItems.length}）`}
     </button>}
     <button type="button" className="button secondary" disabled={!!pending || discovery.kind === 'loading'} onClick={refreshDiscovery}>
      <RefreshCw size={14}/>重新扫描
     </button>
     <button type="button" className="button text-button" onClick={() => setShowImport(false)}>关闭</button>
    </div>
   </div>
   {discovery.kind === 'ready' && discoveredItems.length > 0 ? <ul className="provider-import-list">
    {discoveredItems.map(({ id, source, profile }) => <li key={id}>
     <ConnectionIcon id={resolveIcon(profile)} size={18}/>
     <div className="provider-import-copy">
      <div className="provider-row-title">
       <strong>{profile.name}</strong>
       <span className="provider-tag">{discoverySourceNames[source] ?? source}</span>
      </div>
      <small>{displayEndpoint(profile.baseUrl)}{profile.model ? ` · ${profile.model}` : ''} · {protocolLabels[resolveProtocol(profile)]}</small>
     </div>
     <button type="button" className="button secondary" disabled={!!pending} aria-label={`导入 ${profile.name}`} onClick={() => void importConnection(id)}>
      导入
     </button>
    </li>)}
   </ul> : <p className="provider-empty-inline">当前没有未导入的本机配置。</p>}
  </section>}

  {directoryOpen && <ProviderDirectory
   connections={connections}
   discoveredCount={demo ? 0 : discoveredItems.length}
   showClose={savedProfiles.length > 0}
   onClose={() => setShowDirectory(false)}
   onOpenImport={!demo && discoveredItems.length > 0 ? () => { setShowDirectory(false); setShowImport(true); } : undefined}
   onChoose={(profile, pastedKey) => openEditor(profile, pastedKey)}
  />}
 </section>;
}

export function ModelPicker({ profiles, profileId, model, onSelect, onModel, api, onSettings, demo, disabled }: {
 profiles: Profile[]; profileId: string; model: string; onSelect: (id: string) => void; onModel: (id: string) => void;
 api: API; onSettings: () => void; demo: boolean; disabled: boolean;
}) {
 const [models, setModels] = useState<Model[]>([]);
 const [loading, setLoading] = useState(false);
 const [error, setError] = useState('');
 const profile = profiles.find(p => p.id === profileId);
 const selectable = profiles.filter(p => !p.disabled || p.id === profileId);

 async function load() {
  setLoading(true);
  setError('');
  try { setModels(await api<Model[]>('models', { id: profileId })); }
  catch (err) { setError(`${(err as Error).message}。可手动填写模型 ID。`); }
  finally { setLoading(false); }
 }

 return <div className="reader-model">
  <div className="reader-model-heading"><Cpu size={15}/><strong>处理模型</strong><button className="button text-button" onClick={onSettings}>管理提供商</button></div>
  <label className="reader-model-field">
   <span className="visually-hidden">处理供应商</span>
   {profile && <ConnectionIcon id={resolveIcon(profile)} size={16}/>}
   <select value={profileId} aria-label="处理供应商" onChange={e => onSelect(e.target.value)} disabled={disabled || loading}>
    <option value="">本地摘录</option>
    {selectable.map(p => <option key={p.id} value={p.id}>
     {p.name}{p.disabled ? '（已停用）' : !p.ready ? '（待填密钥）' : ''}
    </option>)}
   </select>
  </label>
  {profile && <div className="model-catalog">
   <label><span className="visually-hidden">处理模型 ID</span><input aria-label="处理模型 ID" placeholder="输入模型 ID" value={model} onChange={e => onModel(e.target.value)} disabled={disabled}/></label>
   <button className="button secondary" disabled={disabled || loading || !profile.ready} onClick={() => void load()}>{loading && <EagleMark flying/>}{loading ? '读取中' : '读取模型'}</button>
  </div>}
  {error && <p className="inline-error" role="alert">{error}</p>}
  {profile && models.length > 0 && <Catalog models={models} value={model} onSelect={onModel} label="选择目录中的模型" disabled={disabled}/>}
  {profile && reasoningBadgeLabel(profile.reasoningEffort) && <small className="muted">{reasoningBadgeLabel(profile.reasoningEffort)}</small>}
  {!demo && !profileId && selectable.length === 0 && <p className="muted"><Database size={13}/>添加算力提供商后，可用它提炼当前会话。</p>}
 </div>;
}
