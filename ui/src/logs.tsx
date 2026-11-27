import { useEffect, useMemo, useRef, useState } from 'react';
import {
 ArrowUpRight,
 Check,
 ChevronDown,
 ChevronRight,
 Clock3,
 Copy,
 ListTree,
 RefreshCw,
 Search,
 X,
} from 'lucide-react';
import { DateRangePicker, dateValue, dayStart, rangeLabel, type DateInterval } from './date-range';
import { EagleLoading } from './eagle';
import { Pagination } from './pagination';

const areas = {
 review: { label: '工作回顾', hint: '自动提炼与回顾校验' },
 scan: { label: '会话扫描', hint: '本地与 WSL 会话发现' },
 model: { label: '模型连接', hint: '连通性测试与模型列表' },
} as const;

const areaOrder: Array<keyof typeof areas> = ['review', 'scan', 'model'];

const levels = {
 error: '错误',
 warning: '警告',
 info: '信息',
} as const;

const levelOrder: Array<keyof typeof levels> = ['error', 'warning', 'info'];

type AreaId = keyof typeof areas;
type LevelId = keyof typeof levels;
type GroupBy = 'day' | 'area';
type TimeRange = 'all' | 1 | 7 | 30 | DateInterval;

type Event = {
 id: number;
 time: string;
 area: AreaId;
 level: LevelId;
 action: string;
 message?: string;
 sessionId?: string;
 retryAt?: string;
};

type LogsCounts = {
 all: number;
 levels: Record<LevelId, number>;
 areas: Record<AreaId, number>;
};

type LogsResponse = {
 events: Event[];
 total: number;
 page: number;
 pageSize: number;
 counts: LogsCounts;
};

type LogGroup = {
 key: string;
 title: string;
 subtitle: string;
 events: Event[];
 errors: number;
 warnings: number;
};

const timePresets = [
 { value: 'all', label: '全部时间' },
 { value: 1, label: '今天' },
 { value: 7, label: '近 7 天' },
 { value: 30, label: '近 30 天' },
] as const;

const fullTimeFormat = new Intl.DateTimeFormat('zh-CN', { dateStyle: 'short', timeStyle: 'medium', hour12: false });
const clockFormat = new Intl.DateTimeFormat('zh-CN', { hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false });
const shortDateClockFormat = new Intl.DateTimeFormat('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false });
const dayTitleFormat = new Intl.DateTimeFormat('zh-CN', { year: 'numeric', month: 'long', day: 'numeric' });
const weekdayFormat = new Intl.DateTimeFormat('zh-CN', { weekday: 'long' });

const messagePreviewLimit = 220;

function resolveTimeBounds(range: TimeRange): { from?: string; to?: string; label: string } {
 if (range === 'all') return { label: '全部时间' };
 if (typeof range === 'number') {
  const from = dayStart(1 - range);
  const to = dayStart(1);
  const label = timePresets.find(item => item.value === range)?.label ?? `近 ${range} 天`;
  return { from: from.toISOString(), to: to.toISOString(), label };
 }
 const from = new Date(`${range.from}T00:00:00`);
 const to = new Date(`${range.to}T00:00:00`);
 to.setDate(to.getDate() + 1);
 return { from: from.toISOString(), to: to.toISOString(), label: rangeLabel(range) };
}

function dayGroupMeta(date: Date, todayKey: string, yesterdayKey: string) {
 const key = dateValue(date);
 const fullDate = dayTitleFormat.format(date);
 const weekday = weekdayFormat.format(date);
 if (key === todayKey) return { key, title: '今天', subtitle: `${fullDate} · ${weekday}` };
 if (key === yesterdayKey) return { key, title: '昨天', subtitle: `${fullDate} · ${weekday}` };
 return { key, title: fullDate, subtitle: weekday };
}

function groupEvents(events: Event[], groupBy: GroupBy): LogGroup[] {
 const todayKey = dateValue(dayStart(0));
 const yesterdayKey = dateValue(dayStart(-1));
 const buckets = new Map<string, LogGroup>();

 for (const event of events) {
  let key: string;
  let title: string;
  let subtitle: string;
  if (groupBy === 'day') {
   const parsed = new Date(event.time);
   if (Number.isNaN(parsed.getTime())) {
    key = 'unknown';
    title = '未知日期';
    subtitle = '';
   } else {
    ({ key, title, subtitle } = dayGroupMeta(parsed, todayKey, yesterdayKey));
   }
  } else {
   const meta = areas[event.area];
   key = event.area;
   title = meta?.label ?? event.area;
   subtitle = meta?.hint ?? '';
  }

  let group = buckets.get(key);
  if (!group) {
   group = { key, title, subtitle, events: [], errors: 0, warnings: 0 };
   buckets.set(key, group);
  }
  group.events.push(event);
  if (event.level === 'error') group.errors++;
  else if (event.level === 'warning') group.warnings++;
 }

 if (groupBy === 'area') {
  return areaOrder.map(id => buckets.get(id)).filter((item): item is LogGroup => !!item);
 }
 return [...buckets.values()];
}

function formatCopyText(event: Event, sessionTitle?: string) {
 const lines = [
  `[${levels[event.level] ?? event.level}] ${areas[event.area]?.label ?? event.area} · ${event.action}`,
  `时间：${fullTimeFormat.format(new Date(event.time))}`,
 ];
 if (event.message) lines.push(`详情：${event.message}`);
 if (event.retryAt) lines.push(`可自动重试时间：${fullTimeFormat.format(new Date(event.retryAt))}`);
 if (event.sessionId) {
  lines.push(sessionTitle ? `会话：${sessionTitle} (${event.sessionId})` : `会话 ID：${event.sessionId}`);
 }
 return lines.join('\n');
}

function isLongMessage(message: string) {
 return message.length > messagePreviewLimit || message.split('\n').length > 3;
}

function LogEntryRow({
 event,
 groupBy,
 sessionTitle,
 open,
}: {
 event: Event;
 groupBy: GroupBy;
 sessionTitle?: string;
 open: (id: string) => void;
}) {
 const [expanded, setExpanded] = useState(false);
 const [copied, setCopied] = useState(false);
 const copyTimer = useRef<ReturnType<typeof setTimeout>>(null);

 useEffect(() => () => {
  if (copyTimer.current) clearTimeout(copyTimer.current);
 }, []);

 const parsed = new Date(event.time);
 const validTime = !Number.isNaN(parsed.getTime());
 const stamp = validTime ? (groupBy === 'day' ? clockFormat.format(parsed) : shortDateClockFormat.format(parsed)) : event.time;
 const fullStamp = validTime ? fullTimeFormat.format(parsed) : event.time;
 const longMsg = !!event.message && isLongMessage(event.message);

 async function copyEntry() {
  try {
   await navigator.clipboard.writeText(formatCopyText(event, sessionTitle));
   setCopied(true);
   if (copyTimer.current) clearTimeout(copyTimer.current);
   copyTimer.current = setTimeout(() => setCopied(false), 1600);
  } catch {
   // Clipboard unavailable in restricted context.
  }
 }

 return <li className="log-entry">
  <div className="log-entry-meta">
   <time className="log-entry-stamp" dateTime={event.time} title={fullStamp}>{stamp}</time>
   <span className="log-entry-level" data-level={event.level}>
    <span className="log-dot" data-level={event.level} aria-hidden="true"/>
    <span>{levels[event.level]}</span>
   </span>
  </div>
  <article className="log-entry-body">
   <div className="log-entry-head">
    <h4>{event.action}</h4>
    <div className="log-entry-tags">
     {groupBy === 'day' && <span className="log-area-tag">{areas[event.area].label}</span>}
     <button type="button" className="log-copy-btn" aria-label={`复制「${event.action}」日志`} onClick={() => void copyEntry()}>
      {copied ? <><Check size={13} aria-hidden="true"/><span>已复制</span></> : <><Copy size={13} aria-hidden="true"/><span>复制</span></>}
     </button>
    </div>
   </div>
   {event.message && <div className="log-entry-message-wrap">
    <p className={`log-entry-message${longMsg && !expanded ? ' is-clamped' : ''}`}>{event.message}</p>
    {longMsg && <button type="button" className="text-button log-expand-btn" onClick={() => setExpanded(value => !value)}>
     {expanded ? '收起详情' : '展开全文'}
    </button>}
   </div>}
   {(event.retryAt || event.sessionId) && <div className="log-entry-foot">
    {event.retryAt && <span className="log-retry-note">
     <Clock3 size={13} aria-hidden="true"/>
     <span>可自动重试时间 <time dateTime={event.retryAt}>{fullTimeFormat.format(new Date(event.retryAt))}</time></span>
    </span>}
    {event.sessionId && (sessionTitle ?
     <button type="button" className="button text-button log-session-link" title={event.sessionId} onClick={() => open(event.sessionId!)}>
      <span>关联会话：{sessionTitle}</span><ArrowUpRight size={13} aria-hidden="true"/>
     </button> :
     <span className="log-session-raw">会话 <code>{event.sessionId}</code></span>)}
   </div>}
  </article>
 </li>;
}

export function PlatformLogs({ api, sessions, reviewOnly, open }: {
 api: <T>(path: string, data?: unknown, signal?: AbortSignal) => Promise<T>;
 sessions: ReadonlyMap<string, { title: string }>;
 reviewOnly: boolean;
 open: (id: string) => void;
}) {
 const [area, setArea] = useState<AreaId | ''>(reviewOnly ? 'review' : '');
 const [level, setLevel] = useState<LevelId | ''>(reviewOnly ? 'error' : '');
 const [timeRange, setTimeRange] = useState<TimeRange>('all');
 const [groupBy, setGroupBy] = useState<GroupBy>('day');
 const [searchInput, setSearchInput] = useState('');
 const [keyword, setKeyword] = useState('');
 const [page, setPage] = useState(1);
 const [pageSize, setPageSize] = useState(20);
 const [refresh, setRefresh] = useState(0);
 const [collapsedGroups, setCollapsedGroups] = useState<Set<string>>(() => new Set());

 const [data, setData] = useState<LogsResponse>();
 const [loading, setLoading] = useState(true);
 const [error, setError] = useState('');
 const pageTopRef = useRef<HTMLDivElement>(null);

 useEffect(() => {
  const timer = setTimeout(() => {
   const next = searchInput.trim();
   setKeyword(current => {
    if (current !== next) setPage(1);
    return next;
   });
  }, 220);
  return () => clearTimeout(timer);
 }, [searchInput]);

 const timeBounds = useMemo(() => resolveTimeBounds(timeRange), [timeRange]);

 useEffect(() => {
  const controller = new AbortController();
  setLoading(true);
  setError('');
  const params = new URLSearchParams({
   page: String(page),
   pageSize: String(pageSize),
  });
  if (area) params.set('area', area);
  if (level) params.set('level', level);
  if (timeBounds.from) params.set('from', timeBounds.from);
  if (timeBounds.to) params.set('to', timeBounds.to);
  if (keyword) params.set('q', keyword);

  api<LogsResponse>(`logs?${params}`, undefined, controller.signal)
   .then(response => {
    if (controller.signal.aborted) return;
    setData(response);
    if (response.page !== page) setPage(response.page);
    setLoading(false);
   })
   .catch((err: unknown) => {
    if (controller.signal.aborted) return;
    setError(err instanceof Error ? err.message : '无法读取平台日志');
    setLoading(false);
   });
  return () => controller.abort();
 }, [api, area, level, timeBounds.from, timeBounds.to, keyword, page, pageSize, refresh]);

 const groups = useMemo(() => groupEvents(data?.events ?? [], groupBy), [data?.events, groupBy]);

 const counts = data?.counts ?? {
  all: 0,
  levels: { error: 0, warning: 0, info: 0 },
  areas: { review: 0, scan: 0, model: 0 },
 };

 const levelTotal = counts.levels.error + counts.levels.warning + counts.levels.info;
 const areaTotal = counts.areas.review + counts.areas.scan + counts.areas.model;

 const levelItems: Array<{ id: LevelId | ''; label: string; count: number }> = [
  { id: '', label: '全部级别', count: levelTotal },
  ...levelOrder.map(id => ({ id, label: levels[id], count: counts.levels[id] ?? 0 })),
 ];

 const areaItems: Array<{ id: AreaId | ''; label: string; count: number }> = [
  { id: '', label: '全部模块', count: areaTotal },
  ...areaOrder.map(id => ({ id, label: areas[id].label, count: counts.areas[id] ?? 0 })),
 ];

 const customCalendarInterval: DateInterval = useMemo(() => {
  if (typeof timeRange === 'object') return timeRange;
  if (typeof timeRange === 'number') {
   return { from: dateValue(dayStart(1 - timeRange)), to: dateValue(dayStart(0)) };
  }
  return { from: dateValue(dayStart(-6)), to: dateValue(dayStart(0)) };
 }, [timeRange]);

 const hasFilter = area !== '' || level !== '' || timeRange !== 'all' || keyword !== '';
 const allGroupsCollapsed = groups.length > 0 && groups.every(group => collapsedGroups.has(group.key));

 function selectLevel(next: LevelId | '') {
  setLevel(current => (next !== '' && current === next ? '' : next));
  setPage(1);
 }

 function selectArea(next: AreaId | '') {
  setArea(current => (next !== '' && current === next ? '' : next));
  setPage(1);
 }

 function selectTimeRange(next: TimeRange) {
  setTimeRange(next);
  setPage(1);
 }

 function clearFilters() {
  setArea('');
  setLevel('');
  setTimeRange('all');
  setSearchInput('');
  setKeyword('');
  setPage(1);
 }

 function toggleGroup(key: string) {
  setCollapsedGroups(current => {
   const next = new Set(current);
   if (next.has(key)) next.delete(key);
   else next.add(key);
   return next;
  });
 }

 function toggleAllGroups() {
  if (allGroupsCollapsed) {
   setCollapsedGroups(new Set());
  } else {
   setCollapsedGroups(new Set(groups.map(group => group.key)));
  }
 }

 function handlePageChange(nextPage: number) {
  setPage(nextPage);
  pageTopRef.current?.scrollIntoView({ behavior: 'smooth', block: 'start' });
 }

 function handlePageSizeChange(nextSize: number) {
  setPageSize(nextSize);
  setPage(1);
 }

 const activeFilterTags: Array<{ key: string; label: string; onRemove: () => void }> = [];
 if (level) activeFilterTags.push({ key: 'level', label: `级别：${levels[level]}`, onRemove: () => selectLevel('') });
 if (area) activeFilterTags.push({ key: 'area', label: `模块：${areas[area].label}`, onRemove: () => selectArea('') });
 if (timeRange !== 'all') activeFilterTags.push({ key: 'time', label: `时间：${timeBounds.label}`, onRemove: () => selectTimeRange('all') });
 if (keyword) activeFilterTags.push({ key: 'q', label: `关键词：${keyword}`, onRemove: () => { setSearchInput(''); setKeyword(''); setPage(1); } });

 return <div className="logs-page" ref={pageTopRef}>
  <div className="logs-toolbar" role="region" aria-label="日志时间与展示控制">
   <div className="review-time logs-time-bar">
    <div className="segments" role="group" aria-label="时间范围">
     {timePresets.map(({ value, label }) => <button key={String(value)} type="button"
      aria-pressed={timeRange === value} onClick={() => selectTimeRange(value)}>{label}</button>)}
    </div>
    <DateRangePicker range={customCalendarInterval} active={typeof timeRange === 'object'} showRange
     onChange={next => selectTimeRange(next)}/>
   </div>

   <div className="logs-toolbar-right">
    <form className="logs-search" onSubmit={event => {
     event.preventDefault();
     const next = searchInput.trim();
     if (next !== keyword) setPage(1);
     setKeyword(next);
    }}>
     <Search size={15} aria-hidden="true"/>
     <input value={searchInput} onChange={event => setSearchInput(event.target.value)}
      aria-label="搜索平台日志" placeholder="搜索动作、详情或会话 ID"/>
     {searchInput && <button type="button" className="provider-clear-btn" aria-label="清空搜索"
      onClick={() => { setSearchInput(''); setKeyword(''); setPage(1); }}><X size={14}/></button>}
    </form>

    <div className="segments" role="group" aria-label="分组方式">
     <button type="button" aria-pressed={groupBy === 'day'} onClick={() => { setGroupBy('day'); setCollapsedGroups(new Set()); }}>按日期</button>
     <button type="button" aria-pressed={groupBy === 'area'} onClick={() => { setGroupBy('area'); setCollapsedGroups(new Set()); }}>按模块</button>
    </div>

    <button type="button" className="button secondary logs-refresh-btn" disabled={loading}
     onClick={() => setRefresh(value => value + 1)}>
     <RefreshCw size={15} className={loading ? 'logs-spin' : undefined} aria-hidden="true"/><span>刷新</span>
    </button>
   </div>
  </div>

  <div className="logs-compact-filters" aria-label="快捷筛选">
   <div className="changelog-pill-strip" role="group" aria-label="按级别排查">
    {levelItems.map(item => <button key={item.id || 'all'} type="button" className="changelog-pill"
     aria-pressed={level === item.id} onClick={() => selectLevel(item.id)}>
     <span className="log-dot" data-level={item.id || 'all'} aria-hidden="true"/>
     <span>{item.label}</span>
     <span className="changelog-pill-count">{item.count}</span>
    </button>)}
   </div>
   <div className="changelog-pill-strip" role="group" aria-label="按模块排查">
    {areaItems.map(item => <button key={item.id || 'all'} type="button" className="changelog-pill"
     aria-pressed={area === item.id} onClick={() => selectArea(item.id)}>
     <span>{item.label}</span>
     <span className="changelog-pill-count">{item.count}</span>
    </button>)}
   </div>
  </div>

  <div className="logs-layout">
   <section className="logs-main" aria-label="日志记录列表" aria-busy={loading}>
    {error && <div className="feedback error" role="alert">
     <span>{error}</span>
     <button type="button" className="button secondary" onClick={() => setRefresh(value => value + 1)}>重试</button>
    </div>}

    {data && <div className="logs-status-bar">
     <div className="logs-status-left">
      <span className="logs-total-summary" role="status">
       {data.total > 0 ? `共 ${data.total} 条日志` : '0 条日志'}
       {groups.length > 0 && ` · 本页 ${groups.length} 个${groupBy === 'day' ? '日期' : '模块'}分组`}
      </span>
      {activeFilterTags.length > 0 && <div className="logs-active-chips">
       {activeFilterTags.map(tag => <span key={tag.key} className="logs-chip">
        <span>{tag.label}</span>
        <button type="button" aria-label={`移除筛选 ${tag.label}`} onClick={tag.onRemove}><X size={12}/></button>
       </span>)}
       <button type="button" className="text-button logs-clear-btn" onClick={clearFilters}>清除全部</button>
      </div>}
     </div>
     {groups.length > 1 && <button type="button" className="text-button logs-fold-all" onClick={toggleAllGroups}>
      {allGroupsCollapsed ? '展开全部分组' : '收起全部分组'}
     </button>}
    </div>}

    {!data && loading ? <EagleLoading label="正在读取平台日志"/> :
     !data ? null :
     data.events.length === 0 ? <div className="empty logs-empty">
      <span className="empty-icon"><ListTree size={22}/></span>
      <h3>{hasFilter ? '当前筛选下没有匹配的日志' : '还没有平台运行日志'}</h3>
      <p>{hasFilter ? '可以切换右侧级别、模块，或放宽时间范围与关键词。' : '会话扫描、自动回顾与模型连接测试的记录会自动沉淀在这里。'}</p>
      {hasFilter && <button type="button" className="button secondary" onClick={clearFilters}>重置筛选</button>}
     </div> :
     <div className={`logs-feed${loading ? ' is-loading' : ''}`}>
      {groups.map(group => {
       const collapsed = collapsedGroups.has(group.key);
       return <section key={group.key} className="logs-group-card" aria-label={`${group.title}（${group.events.length} 条）`}>
        <button type="button" className="logs-group-head" aria-expanded={!collapsed} onClick={() => toggleGroup(group.key)}>
         <span className="logs-group-chevron" aria-hidden="true">
          {collapsed ? <ChevronRight size={16}/> : <ChevronDown size={16}/>}
         </span>
         <div className="logs-group-title">
          <h3>{group.title}</h3>
          {group.subtitle && <span className="logs-group-sub">{group.subtitle}</span>}
         </div>
         <div className="logs-group-badges">
          {group.errors > 0 && <span className="logs-mini-count" data-level="error">{group.errors} 错误</span>}
          {group.warnings > 0 && <span className="logs-mini-count" data-level="warning">{group.warnings} 警告</span>}
          <span className="logs-group-total">{group.events.length} 条</span>
         </div>
        </button>
        {!collapsed && <ol className="logs-entries">
         {group.events.map(event => <LogEntryRow key={event.id} event={event} groupBy={groupBy}
          sessionTitle={event.sessionId ? sessions.get(event.sessionId)?.title : undefined} open={open}/>)}
        </ol>}
       </section>;
      })}
     </div>}

    {data && data.total > 0 && <Pagination page={data.page} pageSize={data.pageSize} total={data.total}
     disabled={loading} onPageChange={handlePageChange} onPageSizeChange={handlePageSizeChange}/>}
   </section>

   <aside className="logs-sidebar" aria-label="日志筛选面板">
    <section className="changelog-side-card logs-side-filter" aria-labelledby="logs-level-heading">
     <div className="changelog-side-head">
      <h3 id="logs-level-heading">按级别排查</h3>
      {level && <button type="button" className="text-button" onClick={() => selectLevel('')}>全部</button>}
     </div>
     <div className="changelog-side-list">
      {levelItems.map(item => <button key={item.id || 'all'} type="button" className="changelog-filter-row"
       data-empty={item.count === 0 ? 'true' : undefined}
       aria-pressed={level === item.id} onClick={() => selectLevel(item.id)}>
       <span className="log-dot" data-level={item.id || 'all'} aria-hidden="true"/>
       <span className="changelog-filter-label">{item.label}</span>
       <span className="changelog-filter-count">{item.count}</span>
      </button>)}
     </div>
    </section>

    <section className="changelog-side-card logs-side-filter" aria-labelledby="logs-area-heading">
     <div className="changelog-side-head">
      <h3 id="logs-area-heading">按模块排查</h3>
      {area && <button type="button" className="text-button" onClick={() => selectArea('')}>全部</button>}
     </div>
     <div className="changelog-side-list">
      {areaItems.map(item => <button key={item.id || 'all'} type="button" className="changelog-filter-row"
       data-empty={item.count === 0 ? 'true' : undefined}
       aria-pressed={area === item.id} onClick={() => selectArea(item.id)}>
       <span className="changelog-filter-label">{item.label}</span>
       <span className="changelog-filter-count">{item.count}</span>
      </button>)}
     </div>
    </section>

    <section className="changelog-side-card logs-side-note" aria-labelledby="logs-retention-heading">
     <h3 id="logs-retention-heading">排查说明</h3>
     <p>当前范围共 <strong>{counts.all}</strong> 条记录。平台自动保留最近 1,000 条运行事件，按时间倒序分页读取。</p>
     <p>点击右侧级别或模块可组合定位异常；带会话关联的日志可直接打开右侧阅读器核对原文。</p>
     {hasFilter && <button type="button" className="button secondary logs-reset-side" onClick={clearFilters}>重置全部筛选</button>}
    </section>
   </aside>
  </div>
 </div>;
}
