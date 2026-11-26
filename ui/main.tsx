import { useEffect, useId, useMemo, useRef, useState, type FormEvent, type ReactNode } from 'react';
import { Archive, ArchiveRestore, ArrowLeft, ArrowRight, ArrowUpRight, BookOpen, CalendarDays, Check, ChevronDown, ChevronRight, CircleHelp, Copy, FileStack, FileText, Folder, FolderGit2, LayoutList, ListChecks, ListTree, MessagesSquare, PanelLeftClose, PanelLeftOpen, PanelRightClose, PanelRightOpen, Plus, RefreshCw, ScanText, Search, SlidersHorizontal, SquareTerminal, X } from 'lucide-react';
import { ModelPicker, SettingsWorkspace, type Connections, type Profile } from './models';
import { PanelGrip } from './panels';
import { Pagination } from './pagination';
import { AgentBadge, AgentPicker, agentKey, agentName, agentQuery, countAgents, type AgentSource } from './agents';
import { displayPath, displayScanWarning, homeRelativePath, locationKey, locationLabel, type Location } from './paths';
import { EagleLoading, EagleMark } from './eagle';
import { AboutContent, UpdateButton, type About, type Update, type UpdateActivity } from './about';
import { DateRangePicker, ReviewDropdown, dateValue, dayStart, rangeLabel, type DateInterval } from './date-range';
import { PlatformLogs } from './logs';
import { ReportDocument, type WorkReport } from './report-document';

type Page = 'review' | 'search' | 'sessions' | 'notes' | 'reports' | 'logs' | 'settings' | 'about';
type Session = AgentSource & { id: string; project: string; projectPath: string; platform:string; wsl?:string; sourceRoot:string; title: string; cwd: string; path: string; start: string; updated: string; parentId?: string; archived?:boolean; count: number; messages?: Message[] };
type Message = { id: string; role: string; text: string; timestamp: string; line: number };
type ProjectSource = Location & {project:string;projectPath:string};
type Topic = { title: string; problem: string; approach?:string; outcome: string; status: string; lesson: string; exercise: string; evidence: string[]; evidenceVersions?:Record<string,string>; evidenceStale?:boolean };
type ReviewEntry = { session:Session; state:'ready'|'queued'|'running'|'cooldown'|'skipped'|'error'|'disabled'; topics?:Topic[]; autoReviewAt?:string };
type RetryReview = (id:string, incremental:boolean)=>Promise<void>;
type Review = { connectionId:string; error?:string; settingsError?:boolean; entries:ReviewEntry[] };
type Detail = { session: Session; topics: Topic[] | null; generated: boolean; staleAnchor?:boolean };
type DistillPlan = { inputBudget:number; outputBudget:number; totalOutputBudget?:number; thinkingBudget?:number; estimatedInput:number; included:number; total:number; shortened:boolean; confirmation:string };
type Match = { sessionId: string; messageId: string; snippet: string; evidenceVersion?:string };
type Hit = AgentSource & { sessionId: string; project: string; topic: Topic; matches: Match[]; moreMatches?:string[] };
type SearchPage = { hits:Hit[]; page:number; pageSize:number; total:number };
type SearchMatchPage = { matches:Match[]; hasMore:boolean };
type Note = { id: number; sessionId: string; topic: Topic };
type ReaderFocus = {id:string;anchor?:string;tab:'topic'|'raw';evidenceVersion?:string};
type OpenSession = (id:string,anchor?:string,tab?:ReaderFocus['tab'],evidenceVersion?:string)=>void;
type ScanEnvironment = Location & { home?:string; state:string };
type ScanRoot = Location & AgentSource & { directory:string; state:string; sessions:number };
type Coverage = { environments:ScanEnvironment[]; roots:ScanRoot[] };
type Listing = { sessions: Session[]; warnings: string[] | null; coverage?:Coverage };

const launchToken = new URLSearchParams(location.hash.slice(1)).get('open');
if (launchToken) {
 sessionStorage.setItem('knooo-launch', launchToken);
 history.replaceState(null, '', location.pathname + location.search);
}
const token = sessionStorage.getItem('knooo-launch') ?? '';

const nav = [
 { id: 'review', label: '工作回顾', icon: LayoutList }, { id: 'search', label: '搜索', icon: Search },
 { id: 'sessions', label: '会话', icon: MessagesSquare }, { id: 'notes', label: '知识库', icon: BookOpen },
 { id: 'reports', label: '工作报告', icon: FileStack }, { id: 'logs', label: '平台日志', icon: ListTree },
 { id: 'settings', label: '本机设置', icon: SlidersHorizontal },
 { id: 'about', label: '关于', icon: CircleHelp },
] as const;
const narrowLayout = 920;
const leftBounds = {min:164,max:304};
const rightBounds = {min:328,max:684};
function savedWidth(key:string, fallback:number, bounds:{min:number;max:number}) {
 const saved = Number(localStorage.getItem(key));
 return saved ? Math.max(bounds.min,Math.min(bounds.max,saved)) : fallback;
}
const ranges = [{value:1,label:'今天'},{value:3,label:'近 3 天'},{value:7,label:'近 7 天'},{value:30,label:'近 30 天'}] as const;
type ReviewRange = typeof ranges[number]['value'] | DateInterval;
const formatTime = (time: string) => time && new Date(time).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit', hour12: false });
const formatDay = (date: Date) => new Intl.DateTimeFormat('zh-CN', { year: 'numeric', month: 'long', day: 'numeric', weekday: 'long' }).format(date);
const historyDate = new Intl.DateTimeFormat('zh-CN', { year:'numeric', month:'2-digit', day:'2-digit' });
const historyTime = (value:string) => value ? `${historyDate.format(new Date(value))} ${formatTime(value)}` : '';
const topicStatus: Record<string,string> = {verified:'已验证',resolved:'已解决',open:'待处理',observed:'已观察'};

async function api<T>(path: string, data?: unknown, signal?: AbortSignal): Promise<T> {
 const headers = { Authorization: `Bearer ${token}` };
 const response = await fetch(`/api/${path}`, data === undefined ? {signal,headers} : {method:'POST',headers:{...headers,'Content-Type':'application/json'},body:JSON.stringify(data),signal});
 if (!response.ok) throw new Error((await response.text()).trim() || '操作失败');
 return response.json();
}
function Empty({ icon, title, children }: {icon: ReactNode; title: string; children: ReactNode}) { return <div className="empty"><span className="empty-icon">{icon}</span><h3>{title}</h3>{children}</div>; }
function ProjectIcon() { return <span className="project-icon"><FolderGit2 size={18}/></span>; }

const projectKey = (source:ProjectSource) => JSON.stringify([locationKey(source),displayPath(source.projectPath)]);
const projectOrigin = (session:Session) => `${locationLabel(session)}${session.projectPath ? ` · ${displayPath(session.projectPath)}` : ''}`;

function App() {
 const [page, setPage] = useState<Page>(()=>new URLSearchParams(location.search).get('view') === 'sessions' ? 'sessions' : 'review');
 const [reviewLogs,setReviewLogs] = useState(false);
 const [about, setAbout] = useState<About|null>();
 const [update, setUpdate] = useState<Update>();
 const [updateActivity, setUpdateActivity] = useState<UpdateActivity>('idle');
 const [updateError, setUpdateError] = useState('');
 const updateBusy = useRef(false);
 const [listing, setListing] = useState<Listing>({sessions:[],warnings:null});
 const [reviewRange,setReviewRange] = useState<ReviewRange>(1);
 const [reviewProjects,setReviewProjects] = useState<Map<string,Session>>(()=>new Map());
 const [collapsedReviewProjects,setCollapsedReviewProjects] = useState<Set<string>>(()=>new Set());
 const [review, setReview] = useState<Review>();
 const [reviewTick, setReviewTick] = useState(0);
 const [project, setProject] = useState('');
 const [storedAgents, setAgents] = useState<string[]>([]);
 const [query, setQuery] = useState('');
 const [searched, setSearched] = useState('');
 const [searchResult, setSearchResult] = useState<SearchPage>();
 const [searchPosition, setSearchPosition] = useState({scope:'',page:1});
 const [searching, setSearching] = useState(false);
 const [notes, setNotes] = useState<Note[]>();
 const [focus, setFocus] = useState<ReaderFocus>();
 const [viewport,setViewport] = useState(window.innerWidth);
 const [navCollapsed,setNavCollapsed] = useState(localStorage.getItem('knooo-nav-collapsed') === 'true');
 const [readerCollapsed,setReaderCollapsed] = useState(false);
 const [leftWidth,setLeftWidth] = useState(()=>savedWidth('knooo-left-width',204,leftBounds));
 const [rightWidth,setRightWidth] = useState(()=>savedWidth('knooo-right-width',432,rightBounds));
 const [connections,setConnections] = useState<Connections>({profiles:[],presets:[],gatewayExamples:[]});
 const [loadingConnections,setLoadingConnections] = useState(true);
 const [profileId,setProfileId] = useState('');
 const [model,setModel] = useState('');
 const [refreshing, setRefreshing] = useState(false);
 const [loadingListing, setLoadingListing] = useState(true);
 const [ready, setReady] = useState(false);
 const [error, setError] = useState('');
 const [notice, setNotice] = useState('');
 const searchBox = useRef<HTMLInputElement>(null);
 const navToggle = useRef<HTMLButtonElement>(null);
 const readerToggle = useRef<HTMLButtonElement>(null);
 const listingRequest = useRef(0);
 const listingApplied = useRef(0);
 const current = nav.find(item => item.id === page)!;
 const narrow = viewport < narrowLayout;
 const showReader = !!focus && !readerCollapsed && page !== 'settings';
 const left = navCollapsed ? 54 : leftWidth;
 const maxRight = Math.min(rightBounds.max,Math.max(rightBounds.min,viewport-left-358));
 const maxLeft = Math.min(leftBounds.max,Math.max(leftBounds.min,viewport-(showReader ? rightBounds.min : 0)-358));
 const displayLeft = Math.min(left,maxLeft);

 async function checkUpdate() {
  if (updateBusy.current) return;
  updateBusy.current = true;
  setUpdateActivity('checking');
  setUpdateError('');
  try {
   setUpdate(await api<Update>('update'));
  } catch (err) {
   setUpdateError(err instanceof Error ? err.message : '无法检查更新，请稍后重试。');
  } finally {
   updateBusy.current = false;
   setUpdateActivity('idle');
  }
 }

 async function installUpdate() {
  if (updateBusy.current || !update?.available || !update.canInstall) return;
  updateBusy.current = true;
  setUpdateActivity('installing');
  setUpdateError('');
  try {
   await api('update', { version: update.available.version });
   setUpdateActivity('started');
  } catch (err) {
   updateBusy.current = false;
   setUpdateActivity('idle');
   setUpdateError(err instanceof Error ? err.message : '无法安装更新，请稍后重试。');
   setPage('about');
  }
 }
 const displayRight = Math.min(rightWidth,maxRight);
 const profile = connections.profiles.find(item=>item.id===profileId);
 const activeSessions = useMemo(()=>listing.sessions.filter(s=>!s.archived),[listing.sessions]);
 const activeAgents = useMemo(()=>countAgents(activeSessions),[activeSessions]);
 const selectedAgents = useMemo(()=>storedAgents.filter(agent=>activeAgents.has(agent)),[storedAgents,activeAgents]);
 const projects = useMemo(() => [...new Map(activeSessions.filter(s=>s.projectPath).map(s => [projectKey(s),s])).entries()].sort((a,b)=>a[1].project.localeCompare(b[1].project)), [activeSessions]);
 const selectedProject = projects.find(([key])=>key===project)?.[1];
 const projectPath = displayPath(selectedProject?.projectPath ?? '');
 const projectLocation = selectedProject ? locationKey(selectedProject) : '';
 const searchScope = agentQuery({q:searched,project:projectPath,location:projectLocation},selectedAgents).toString();
 const searchPage = searchPosition.scope===searchScope ? searchPosition.page : 1;
 const sessionMap = useMemo(() => new Map(listing.sessions.map(session => [session.id, session])), [listing]);
 const fail = (err:unknown) => setError(err instanceof Error ? err.message : String(err));
 function collapseReader() { setReaderCollapsed(true);readerToggle.current?.focus(); }
 async function retryReviewSession(id: string, incremental: boolean) {
  setError('');
  setNotice('');
  try {
   const result = await api<{queued:boolean}>('reviews/retry', { id, incremental });
   setNotice(result.queued ? '已加入提炼队列，稍后自动更新。' : '这条会话已提炼，或已进入提炼队列。');
   setReviewTick(v => v + 1);
  } catch (err) {
   fail(err);
  }
 }
 async function saveReviewNote(sessionId: string, topic: Topic) {
  await api('notes', { sessionId, topic });
  setNotice('已存入知识库。');
 }
 function navigate(next:Page, fromReview=false) { setPage(next);setReviewLogs(fromReview);setError('');setNotice(''); }
 function open(id:string, anchor?:string, tab:ReaderFocus['tab']=anchor ? 'raw' : 'topic', evidenceVersion?:string) { setFocus({id,anchor,tab,evidenceVersion});setReaderCollapsed(false); }
 function selectProfile(id:string) { setProfileId(id);setModel(connections.profiles.find(p=>p.id===id)?.model ?? ''); }
 async function loadConnections() {
  const next = await api<Connections>('connections');
  setConnections(next);
  if (profileId && !next.profiles.some(item=>item.id===profileId && !item.disabled)) selectProfile('');
 }
 useEffect(()=>{
  const resize = ()=>setViewport(window.innerWidth);
  window.addEventListener('resize',resize);
  return ()=>window.removeEventListener('resize',resize);
 },[]);
 useEffect(()=>localStorage.setItem('knooo-nav-collapsed',String(navCollapsed)),[navCollapsed]);
 useEffect(()=>localStorage.setItem('knooo-left-width',String(leftWidth)),[leftWidth]);
 useEffect(()=>localStorage.setItem('knooo-right-width',String(rightWidth)),[rightWidth]);
 async function loadListing() {
  const seq = ++listingRequest.current;
  const next = await api<Listing>('sessions');
  if (seq > listingApplied.current) {
   listingApplied.current = seq;
   setListing(next);
   setReady(true);
  }
  return next;
 }
 async function refresh() {
  setRefreshing(true);setError('');setNotice('');
  try { await api('refresh',{manual:true}); const next = await loadListing();setNotice(`已发现 ${next.sessions.length} 条会话。`); }
  catch (err) { fail(err); }
  finally { setRefreshing(false); }
 }
 useEffect(() => {
  if (page !== 'review') return;
  const controller = new AbortController();
  let timer: ReturnType<typeof setTimeout>;
  async function poll() {
   let delay = 60000;
   try {
    const from = typeof reviewRange==='number' ? dayStart(1-reviewRange) : new Date(`${reviewRange.from}T00:00:00`);
    const to = typeof reviewRange==='number' ? dayStart() : new Date(`${reviewRange.to}T00:00:00`);
    to.setDate(to.getDate()+1);
    const args = agentQuery({from:from.toISOString(),to:to.toISOString()},selectedAgents);
    const next = await api<Review>(`reviews?${args}`,undefined,controller.signal);
    if (!controller.signal.aborted) {
     setReview(next);
     const hasActive = next.entries.some(entry => entry.state === 'running' || entry.state === 'queued');
     const hasCooldown = next.entries.some(entry => entry.state === 'cooldown');
     delay = hasActive ? 4000 : hasCooldown ? 30000 : 60000;
    }
   } catch (err) {
    if (!controller.signal.aborted) { fail(err); delay = 15000; }
   } finally {
    if (!controller.signal.aborted) {
     timer = setTimeout(() => {
      if (document.hidden && delay >= 30000) timer = setTimeout(poll, delay);
      else void poll();
     }, delay);
    }
   }
  }
  void poll();
  return ()=>{controller.abort();clearTimeout(timer);};
 },[page,reviewRange,selectedAgents,listing,reviewTick]);
 useEffect(() => {
  loadConnections().catch(fail).finally(()=>setLoadingConnections(false));
  api<About>('about').then(value=>{setAbout(value);if (!value.demo) void checkUpdate();}).catch(err=>{setAbout(null);fail(err);});
  loadListing().catch(fail).finally(()=>setLoadingListing(false));
 },[]);
 useEffect(() => {
  if (about?.demo !== false) return;
  const timer = setInterval(() => { if (!document.hidden) void checkUpdate(); }, 60 * 60 * 1000);
  return () => clearInterval(timer);
 },[about?.demo]);
 useEffect(() => {
  if (page!=='sessions' || !ready || about?.demo!==false || refreshing) return;
  let active = true;
  let timer: ReturnType<typeof setTimeout>;
  async function poll() {
   try {
    if (!document.hidden) {
     await api('refresh',{});
     if (!active) return;
     await loadListing();
    }
   } catch {
    // Keep the visible history until the next incremental scan succeeds.
   } finally {
    if (active) timer=setTimeout(poll,15000);
   }
  }
  timer=setTimeout(poll,15000);
  return ()=>{active=false;clearTimeout(timer);};
 },[page,ready,about?.demo,refreshing]);
 useEffect(() => {
  if (page !== 'search' || !searched) return;
  const controller = new AbortController();
  setSearching(true);
  const params = new URLSearchParams(searchScope);
  params.set('page',String(searchPage));
  api<SearchPage>(`search?${params}`,undefined,controller.signal)
   .then(next=>{if (!controller.signal.aborted) setSearchResult(next);})
   .catch(err=>{if (!controller.signal.aborted) {setSearchResult(undefined);fail(err);}})
   .finally(()=>{if (!controller.signal.aborted) setSearching(false);});
  return () => controller.abort();
 },[page,searched,searchScope,searchPage,listing]);
 useEffect(() => {
  if (page!=='notes') return;
  const controller = new AbortController();
  setNotes(undefined);
  api<Note[]>('notes',undefined,controller.signal)
   .then(next=>{if (!controller.signal.aborted) setNotes(next);})
   .catch(err=>{if (!controller.signal.aborted) {setNotes([]);fail(err);}});
  return () => controller.abort();
 },[page]);
 useEffect(() => {
  function shortcut(e: KeyboardEvent) {
   if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase()==='k') { e.preventDefault(); searchBox.current?.focus(); }
   if (e.key === 'Escape' && !e.defaultPrevented && readerToggle.current?.getAttribute('aria-expanded') === 'true') collapseReader();
  }
  document.addEventListener('keydown',shortcut);
  return () => document.removeEventListener('keydown',shortcut);
 },[]);
 function search(e:FormEvent) { e.preventDefault();if (!query.trim()) return;setSearched(query.trim());setSearchPosition({scope:'',page:1});navigate('search'); }
 const filter = <div className="filters"><AgentPicker counts={activeAgents} selected={selectedAgents} onChange={setAgents}/><label className="select"><Folder size={16}/><select aria-label="项目" title={selectedProject ? projectOrigin(selectedProject) : '全部项目'} value={selectedProject ? project : ''} onChange={e=>setProject(e.target.value)}><option value="">全部项目</option>{projects.map(([key,session])=><option key={key} value={key}>{session.project} · {projectOrigin(session)}</option>)}</select><ChevronDown size={13}/></label></div>;
 const scanning = refreshing || loadingListing;
 const refreshAction = about?.demo ? undefined : <button className="button secondary" disabled={scanning} onClick={()=>void refresh()}>{refreshing && ready ? <EagleMark flying/> : <RefreshCw size={16}/>} {scanning ? '正在发现会话' : '刷新会话'}</button>;
 return <div className={`shell ${showReader ? 'with-reader' : ''} ${navCollapsed ? 'nav-compact' : ''}`} style={{gridTemplateColumns:narrow ? undefined : `${displayLeft}px minmax(0,1fr)${showReader ? ` ${displayRight}px` : ''}`}}>
  <aside className="rail" id="navigation" aria-label="主导航">
   <button className="brand" aria-label="Knooo 首页" title="Knooo" onClick={()=>navigate('review')}><EagleMark/><span>Knooo</span></button>
   <nav className="nav-group" aria-label="工作台">{nav.slice(0,5).map(({id,label,icon:Icon})=><button key={id} className={`nav-item ${page===id ? 'active' : ''}`} title={label} aria-label={label} aria-current={page===id ? 'page' : undefined} onClick={()=>navigate(id)}><Icon size={18} strokeWidth={1.75}/><span>{label}</span></button>)}</nav>
   <nav className="nav-group system" aria-label="应用">{nav.slice(5).map(({id,label,icon:Icon})=><button key={id} className={`nav-item ${page===id ? 'active' : ''}`} title={label} aria-label={label} aria-current={page===id ? 'page' : undefined} onClick={()=>navigate(id)}><Icon size={18} strokeWidth={1.75}/><span>{label}</span></button>)}</nav>
   <div className="rail-bottom">{about?.demo && <div><span className="connection-dot"/><span>演示工作区</span></div>}<button onClick={()=>navigate('about')}>v{about?.version ?? '0.1.0'} <ArrowUpRight size={13}/></button></div>
   {!narrow && !navCollapsed && <PanelGrip side="left" width={displayLeft} min={leftBounds.min} max={maxLeft} onResize={setLeftWidth} onToggle={()=>{setNavCollapsed(true);navToggle.current?.focus();}}/>}
  </aside>
  <div className="workbench">
   <header className="topbar">
    <div className="topbar-location">{!narrow && <button ref={navToggle} className="icon-button" aria-controls="navigation" aria-expanded={!navCollapsed} aria-label={navCollapsed ? '展开导航栏' : '收起导航栏'} title={navCollapsed ? '展开导航栏' : '收起导航栏'} onClick={()=>setNavCollapsed(!navCollapsed)}>{navCollapsed ? <PanelLeftOpen size={18}/> : <PanelLeftClose size={18}/>}</button>}<div className="crumb"><current.icon size={17} strokeWidth={1.75}/><span>{current.label}</span></div></div>
    <form className="global-search" onSubmit={search}><Search size={16}/><input ref={searchBox} value={query} onChange={e=>setQuery(e.target.value)} aria-label="搜索会话原文或路径" placeholder="搜索会话原文或路径"/><kbd>Ctrl K</kbd></form>
    {focus && page !== 'settings' && <button ref={readerToggle} className="icon-button restore-reader" aria-controls="session-reader" aria-expanded={showReader} aria-label={showReader ? '收起阅读器' : '展开阅读器'} title={showReader ? '收起阅读器' : '展开阅读器'} onClick={()=>setReaderCollapsed(!readerCollapsed)}>{showReader ? <PanelRightClose size={18}/> : <PanelRightOpen size={18}/>}</button>}
    {about && update?.available && <UpdateButton update={update} activity={updateActivity} repository={about.repository} onInstall={()=>void installUpdate()}/>}
   </header>
   <main className="workspace" id="workspace" data-page={page}>
    {error && <div className="feedback error" role="alert">{error}<button className="icon-button" aria-label="收起提示" onClick={()=>setError('')}><X size={16}/></button></div>}
    {notice && <div className="feedback" role="status"><Check size={16}/>{notice}<button className="icon-button" aria-label="收起提示" onClick={()=>setNotice('')}><X size={16}/></button></div>}
    {page==='review' && <>
     <div className="intro"><div><div className="date-line"><CalendarDays size={15}/>{formatDay(new Date())}</div><h1>工作回顾</h1><p>按项目查看各次会话处理了哪些问题、采取了什么做法，以及结果如何。</p></div>{refreshAction}</div>
     <div className="review-controls"><ReviewDates range={reviewRange} onChange={setReviewRange}/><AgentPicker counts={activeAgents} selected={selectedAgents} onChange={setAgents}/></div>
     {review ? <WorkReview review={review} selected={reviewProjects} onSelect={setReviewProjects}
      collapsed={collapsedReviewProjects} onCollapse={setCollapsedReviewProjects}
      demo={about?.demo===true} onSettings={()=>navigate('settings')} onLogs={()=>navigate('logs',true)}
      onRetry={retryReviewSession} onSaveNote={saveReviewNote} open={open}/> : !error && <EagleLoading label="正在读取工作回顾"/>}
    </>}
    {page==='sessions' && <>
     <Heading title="会话" description={about?.demo ? '示例会话 · 按项目浏览 Agent 历史。' : '自动发现本机 Agent 的会话记录。'} action={refreshAction}/>
     <SessionLibrary listing={listing} scanning={scanning} selected={focus?.id} open={(id,anchor)=>open(id,anchor,'raw')} reload={loadListing} onError={fail} onNotice={setNotice}/>
    </>}
    {page==='search' && <>
     <Heading title="搜索" description="查找会话原文、标题或来源路径。"/>
     <form className="search-page-form" onSubmit={search}><Search size={21}/><input aria-label="搜索关键字" placeholder="输入关键字、错误码或文件路径" value={query} onChange={e=>setQuery(e.target.value)}/><button className="button primary" type="submit">搜索</button></form>
     <div className="review-controls">{filter}</div>
     {searching ? <EagleLoading label="正在搜索会话"/> : searchResult?.hits.map(h=><SearchResult key={h.sessionId} hit={h} sessions={sessionMap} open={open} searchScope={searchScope}/>)}
     {!searching && searchResult && <Pagination label="搜索结果分页" page={searchResult.page} pageSize={searchResult.pageSize} total={searchResult.total} onPageChange={next=>{setError('');setSearchPosition({scope:searchScope,page:next});}}/>}
     {!searching && !error && !searchResult?.hits.length && <Empty icon={<Search size={25}/>} title={searched ? '没有匹配的会话' : '搜索会话'}><p>{searched ? '试试缩短关键字，或调整筛选范围。' : '输入一个工作中记得的关键词。'}</p></Empty>}
    </>}
    {page==='notes' && <><Heading title="知识库" description="把会话里值得复用的经验和练习保存下来。"/>{!notes ? <EagleLoading label="正在读取知识库"/> : notes.map(n=><article className="search-hit" key={n.id}><h3>{n.topic.title}</h3><p>{n.topic.lesson || n.topic.outcome || n.topic.problem}</p>{n.topic.exercise && <p className="muted">练习 · {n.topic.exercise}</p>}{n.topic.evidenceStale && <p className="feedback error">原文依据已变更或需要核对。</p>}<button className="button secondary" onClick={()=>{const id=n.topic.evidenceStale ? undefined : n.topic.evidence[0];open(n.sessionId,id,'raw',id ? n.topic.evidenceVersions?.[id] : undefined);}}>查看原文 <ArrowUpRight size={15}/></button></article>)}{notes?.length===0 && !error && <Empty icon={<BookOpen size={25}/>} title="还没有知识条目"><p>打开一条会话，在主题里点击「存入知识库」。</p><button className="button secondary" onClick={()=>navigate('sessions')}>查看会话 <ArrowRight size={15}/></button></Empty>}</>}
    {page==='reports' && <><Heading title="工作报告" description="从工作回顾提炼所选期间的成果、发现和下一步。"/><Reports listing={listing} agentCounts={activeAgents} selectedAgents={selectedAgents} onAgentsChange={setAgents} profiles={connections.profiles} onOpen={open} fail={fail}/></>}
    {page==='logs' && <><Heading title="平台日志" description="按级别、模块与时间范围排查会话扫描、自动回顾和模型连接记录。"/><PlatformLogs key={String(reviewLogs)} api={api} sessions={sessionMap} reviewOnly={reviewLogs} open={id=>open(id,undefined,'raw')}/></>}
    {page==='settings' && <><Heading title="本机设置" description="管理接入平台的算力提供商与后台默认调用配置。"/>{loadingConnections || about===undefined ? <EagleLoading label="正在读取本机设置"/> : <SettingsWorkspace connections={connections} api={api} onChange={loadConnections} demo={about?.demo===true}/>}</>}
    {page==='about' && <>
     <Heading title="关于"/>
     {about === undefined && <EagleLoading label="正在读取版本信息"/>}
     {about && <AboutContent about={about} update={update} activity={updateActivity} updateError={updateError}
      onCheck={()=>void checkUpdate()} onInstall={()=>void installUpdate()}/>}
    </>}
   </main>
  </div>
  {showReader && narrow && <button className="reader-backdrop" aria-label="收起阅读器" onClick={collapseReader}/>}
  {focus && <Reader key={focus.id} focus={focus} setFocus={setFocus} collapsed={!showReader} close={collapseReader} profile={profile} model={model} demo={about?.demo===true} childrenSessions={listing.sessions.filter(s=>s.parentId===focus.id)} parent={sessionMap.get(sessionMap.get(focus.id)?.parentId ?? '')} open={(id,anchor)=>open(id,anchor,anchor ? 'raw' : focus.tab)}
   panelGrip={showReader && !narrow && <PanelGrip side="right" width={displayRight} min={rightBounds.min} max={maxRight} onResize={setRightWidth} onToggle={collapseReader}/>}
   modelPicker={disabled=><ModelPicker key={profileId} profiles={connections.profiles} profileId={profileId} model={model} onSelect={selectProfile} onModel={setModel} api={api} onSettings={()=>navigate('settings')} demo={about?.demo===true} disabled={disabled}/>}/> }
 </div>;
}

function Heading({ title, description, action }: {title:string;description?:string;action?:ReactNode}) { return <div className="intro standard"><div><h1>{title}</h1>{description && <p>{description}</p>}</div>{action}</div>; }

function ReviewDates({range,onChange}:{range:ReviewRange;onChange:(range:ReviewRange)=>void}) {
 const from = typeof range==='number' ? dateValue(dayStart(1-range)) : range.from;
 const to = typeof range==='number' ? dateValue(dayStart()) : range.to;
 return <div className="review-time">
  <div className="segments" role="group" aria-label="时间范围">{ranges.map(({value,label})=><button key={value} aria-pressed={range===value} onClick={()=>onChange(value)}>{label}</button>)}</div>
  <DateRangePicker range={{from,to}} active={typeof range!=='number'} showRange onChange={onChange}/>
 </div>;
}

const gitMicroStepPattern = /^(?:暂存|提交|推送|拉取|切[换到]?分支|还原.*格式|生成\s*commit|提供\s*commit)/i;
const gitMicroStepDetailPattern = /(?:放入\s*Git?\s*暂存区|commit\s*message|commit\s*信息|推送到远端|提交到\s*Git)/i;

function isSecondaryTopic(topic:Topic) {
 if (topic.status === 'observed') return true;
 const title = topic.title.trim();
 return gitMicroStepPattern.test(title) || gitMicroStepDetailPattern.test(title);
}

type ReviewStatusFilter = 'all' | 'done' | 'open' | 'observed';

function matchesStatusFilter(topic:Topic, filter:ReviewStatusFilter) {
 if (filter === 'all') return true;
 if (filter === 'done') return topic.status === 'verified' || topic.status === 'resolved';
 if (filter === 'open') return topic.status === 'open';
 return topic.status === 'observed';
}

function matchesReviewQuery(topic:Topic, session:Session, query:string) {
 if (!query) return true;
 const q = query.toLowerCase();
 return topic.title.toLowerCase().includes(q) ||
  topic.problem.toLowerCase().includes(q) ||
  (topic.approach?.toLowerCase().includes(q) ?? false) ||
  topic.outcome.toLowerCase().includes(q) ||
  session.title.toLowerCase().includes(q) ||
  session.project.toLowerCase().includes(q);
}

function ReviewTopicCard({topic,sessionTitle,sessionId,demo,onSaveNote,open}:{
 topic:Topic; sessionTitle:string; sessionId:string; demo:boolean;
 onSaveNote:(sessionId:string,topic:Topic)=>Promise<void>;
 open:OpenSession;
}) {
 const [allEvidence,setAllEvidence] = useState(false);
 const [savingNote,setSavingNote] = useState(false);
 const [savedNote,setSavedNote] = useState(false);
 const [saveError,setSaveError] = useState('');
 const heading = topic.title && topic.title!==sessionTitle ? topic.title : topic.problem;
 const visibleEvidence = allEvidence ? topic.evidence : topic.evidence.slice(0,4);
 const hiddenEvidence = topic.evidence.length - visibleEvidence.length;
 async function handleSave() {
  if (savingNote || savedNote) return;
  setSavingNote(true);
  setSaveError('');
  try {
   await onSaveNote(sessionId, topic);
   setSavedNote(true);
  } catch (err) {
   setSaveError(err instanceof Error ? err.message : String(err));
  } finally {
   setSavingNote(false);
  }
 }
 return <li>
  <div className="review-question-title"><h5>{heading}</h5>{topicStatus[topic.status] && <span className="status-label" data-status={topic.status}>{topicStatus[topic.status]}</span>}</div>
  {topic.problem!==heading && <p>{topic.problem}</p>}
  <dl>{topic.approach && <div><dt>做法</dt><dd>{topic.approach}</dd></div>}
   <div><dt>结果</dt><dd>{topic.outcome || '会话中没有确认结果'}</dd></div>
  </dl>
  <div className="review-topic-foot">
   <div className="topic-evidence">
    {visibleEvidence.map((id,index)=><button key={id} className="button text-button" onClick={()=>open(sessionId,id,'raw',topic.evidenceVersions?.[id])}>依据 {index+1} <ArrowUpRight size={13}/></button>)}
    {hiddenEvidence>0 && <button type="button" className="button text-button review-more-evidence" onClick={()=>setAllEvidence(true)}>+{hiddenEvidence} 条依据</button>}
    {allEvidence && topic.evidence.length>4 && <button type="button" className="button text-button review-more-evidence" onClick={()=>setAllEvidence(false)}>收起依据</button>}
   </div>
   {!demo && <button type="button" className={`button text-button review-save-note ${savedNote ? 'is-saved' : ''}`} disabled={savingNote || savedNote} onClick={()=>void handleSave()}>
    {savedNote ? <><Check size={13}/>已存知识库</> : <><BookOpen size={13}/>{savingNote ? '保存中…' : '存入知识库'}</>}
   </button>}
  </div>
  {saveError && <p className="feedback error" role="alert">{saveError}</p>}
 </li>;
}

function reviewCountdown(deadline:number, now:number) {
 const minutes = Math.ceil((deadline-now)/60000);
 if (minutes<=0) return '等待后台调度';
 if (minutes===1) return '不到 1 分钟后自动提炼';
 const hours = Math.floor(minutes/60);
 return `${hours ? `${hours} 小时 ` : ''}${minutes%60 ? `${minutes%60} 分钟` : ''}`.trim() + '后自动提炼';
}

function ReviewRunButton({entry,onRetry}: {entry:ReviewEntry; onRetry:RetryReview}) {
 const [submitting,setSubmitting] = useState(false);
 const rebuild = entry.state==='ready';
 if (entry.state==='running' || entry.state==='queued') return <span className="review-session-count">{entry.state==='running' ? '提炼中…' : '已排队'}</span>;
 async function run() {
  setSubmitting(true);
  try { await onRetry(entry.session.id,!rebuild); }
  finally { setSubmitting(false); }
 }
 return <button type="button" className={`button ${rebuild ? 'text-button review-retry-btn' : 'secondary'}`} disabled={submitting}
  title={rebuild ? '从头重新提炼这条会话' : undefined} onClick={()=>void run()}>
  <RefreshCw size={13}/>{submitting ? '正在提交…' : rebuild ? '重新提炼' : '立即提炼'}
 </button>;
}

function ReviewSessionBlock({entry,collapsed,onToggle,focusedMode,statusFilter,searchTerm,demo,canRetry,now,onRetry,onSaveNote,open}:{
 entry:ReviewEntry; collapsed:boolean; onToggle:()=>void; focusedMode:boolean; statusFilter:ReviewStatusFilter; searchTerm:string;
 demo:boolean; canRetry:boolean; now:number;
 onRetry:RetryReview;
 onSaveNote:(sessionId:string,topic:Topic)=>Promise<void>;
 open:OpenSession;
}) {
 const [showAllPrimary,setShowAllPrimary] = useState(false);
 const [showSecondary,setShowSecondary] = useState(false);
 const {session,topics=[]} = entry;
 const filtered = topics.filter(topic => matchesStatusFilter(topic, statusFilter) && matchesReviewQuery(topic, session, searchTerm));
 if (filtered.length === 0) return null;

 const effectiveFocused = focusedMode && statusFilter === 'all' && !searchTerm;
 const primary:Topic[] = [];
 const secondary:Topic[] = [];
 for (const topic of filtered) {
  if (isSecondaryTopic(topic)) secondary.push(topic);
  else primary.push(topic);
 }
 const primaryLimit = 5;
 const fallbackLimit = 2;
 let shown:Topic[];
 let hiddenPrimary = 0;
 let foldedSecondary:Topic[] = [];
 if (!effectiveFocused) {
  shown = filtered;
 } else if (primary.length > 0) {
  shown = showAllPrimary ? primary : primary.slice(0, primaryLimit);
  hiddenPrimary = primary.length - shown.length;
  foldedSecondary = secondary;
 } else {
  shown = showSecondary ? secondary : secondary.slice(0, fallbackLimit);
  foldedSecondary = showSecondary ? [] : secondary.slice(fallbackLimit);
 }
 const summaryParts = primary.length > 0
  ? [`${primary.length} 项重点`, secondary.length > 0 ? `${secondary.length} 条观察/微步骤` : ''].filter(Boolean)
  : [`${secondary.length} 条观察`];

 return <article className={`review-session ${collapsed ? 'is-collapsed' : ''}`}>
  <div className="review-session-heading">
   <button type="button" className="review-session-toggle" aria-expanded={!collapsed} title={collapsed ? '点击展开该会话条目' : '点击收起该会话条目'} onClick={onToggle}>
    <div className="review-session-badges">
     <AgentBadge source={session}/>
     <span className="review-session-count">{summaryParts.join(' · ')}</span>
     {session.updated && <span className="review-session-time">{historyTime(session.updated)}</span>}
     {entry.autoReviewAt && <span className="review-session-time">{reviewCountdown(Date.parse(entry.autoReviewAt),now)}</span>}
     {collapsed && <span className="review-session-folded-pill">已收起</span>}
    </div>
    <h4>{session.title}</h4>
   </button>
   <div className="review-session-actions">
    {canRetry && <ReviewRunButton entry={entry} onRetry={onRetry}/>}
    <button className="button secondary" onClick={()=>open(session.id,undefined,'raw')}>查看会话 <ArrowUpRight size={14}/></button>
   </div>
  </div>
  {!collapsed && <>
   <ul className="review-questions">{shown.map((topic,index)=><ReviewTopicCard key={`${topic.title}-${index}`} topic={topic} sessionTitle={session.title} sessionId={session.id} demo={demo} onSaveNote={onSaveNote} open={open}/>)}</ul>
   {effectiveFocused && hiddenPrimary > 0 && <button type="button" className="button text-button review-fold-toggle" onClick={()=>setShowAllPrimary(true)}>
    <ChevronDown size={14}/>展开其余 {hiddenPrimary} 项重点工作
   </button>}
   {effectiveFocused && primary.length > primaryLimit && showAllPrimary && <button type="button" className="button text-button review-fold-toggle" onClick={()=>setShowAllPrimary(false)}>
    <ChevronRight size={14}/>收起多余重点工作
   </button>}
   {effectiveFocused && primary.length > 0 && foldedSecondary.length > 0 && <div className="review-secondary-drawer">
    <button type="button" className="review-secondary-toggle" aria-expanded={showSecondary} onClick={()=>setShowSecondary(v=>!v)}>
     {showSecondary ? <ChevronDown size={14}/> : <ChevronRight size={14}/>}
     <span>{showSecondary ? '收起背景观察与微步骤' : `另有 ${foldedSecondary.length} 条背景观察与微步骤`}</span>
    </button>
    {showSecondary && <ul className="review-questions is-secondary">{foldedSecondary.map((topic,index)=><ReviewTopicCard key={`sec-${topic.title}-${index}`} topic={topic} sessionTitle={session.title} sessionId={session.id} demo={demo} onSaveNote={onSaveNote} open={open}/>)}</ul>}
   </div>}
   {effectiveFocused && primary.length === 0 && secondary.length > fallbackLimit && <button type="button" className="button text-button review-fold-toggle" onClick={()=>setShowSecondary(v=>!v)}>
    {showSecondary ? <><ChevronRight size={14}/>收起其余观察</> : <><ChevronDown size={14}/>展开其余 {secondary.length - fallbackLimit} 条观察</>}
   </button>}
  </>}
 </article>;
}

function WorkReview({review,selected,onSelect,collapsed,onCollapse,demo,onSettings,onLogs,onRetry,onSaveNote,open}: {
 review:Review; selected:Map<string,Session>; onSelect:(next:Map<string,Session>)=>void;
 collapsed:Set<string>; onCollapse:(next:Set<string>)=>void;
 demo:boolean; onSettings:()=>void; onLogs:()=>void;
 onRetry:RetryReview;
 onSaveNote:(sessionId:string,topic:Topic)=>Promise<void>;
 open:OpenSession;
}) {
 const [viewMode,setViewMode] = useState<'focused'|'all'>('focused');
 const [statusFilter,setStatusFilter] = useState<ReviewStatusFilter>('all');
 const [searchQuery,setSearchQuery] = useState('');
 const [showUnreviewed,setShowUnreviewed] = useState<boolean>();
 const [collapsedSessions,setCollapsedSessions] = useState<Set<string>>(()=>new Set());
 const [now,setNow] = useState(Date.now);

 const options = new Map(selected);
 for (const {session} of review.entries) options.set(projectKey(session),session);
 const visible = selected.size ? review.entries.filter(({session})=>selected.has(projectKey(session))) : review.entries;
 const ready = visible.filter(entry=>entry.state==='ready').length;
 const pending = visible.filter(entry=>entry.state==='queued'||entry.state==='running').length;
 const cooling = visible.filter(entry=>entry.state==='cooldown').length;
 const unreviewed = visible.filter(entry=> !entry.topics?.length && entry.state!=='ready' && entry.state!=='disabled');
 const nextReviewAt = visible.reduce((next,{autoReviewAt})=>autoReviewAt ? Math.min(next,Date.parse(autoReviewAt)) : next,Infinity);
 const running = visible.some(entry=>entry.state==='running');
 useEffect(()=>{
  if (!Number.isFinite(nextReviewAt)) return;
  setNow(Date.now());
  const timer = setInterval(()=>setNow(Date.now()),15000);
  return ()=>clearInterval(timer);
 },[nextReviewAt]);

 let doneCount = 0;
 let openCount = 0;
 let observedCount = 0;
 const projectKeysWithTopics = new Set<string>();
 const searchTerm = searchQuery.trim();
 const groups = new Map<string,ReviewEntry[]>();
 const visibleSessionIds:string[] = [];
 for (const entry of visible) {
  if (!entry.topics?.length) continue;
  const pKey = projectKey(entry.session);
  projectKeysWithTopics.add(pKey);
  for (const topic of entry.topics) {
   if (topic.status === 'verified' || topic.status === 'resolved') doneCount++;
   else if (topic.status === 'open') openCount++;
   else if (topic.status === 'observed') observedCount++;
  }
  const hasMatch = entry.topics.some(topic => matchesStatusFilter(topic, statusFilter) && matchesReviewQuery(topic, entry.session, searchTerm));
  if (!hasMatch) continue;
  visibleSessionIds.push(entry.session.id);
  const group = groups.get(pKey);
  if (group) group.push(entry);
  else groups.set(pKey,[entry]);
 }
 const totalTopics = doneCount + openCount + observedCount;
 const canRetry = !demo && !!review.connectionId;
 const unreviewedExpanded = showUnreviewed ?? totalTopics===0;
 const hasActiveFilter = statusFilter !== 'all' || !!searchTerm;
 const allSessionsCollapsed = visibleSessionIds.length > 0 && visibleSessionIds.every(id => collapsedSessions.has(id));
 function toggleSessionFold(id:string) {
  setCollapsedSessions(current => {
   const next = new Set(current);
   if (next.has(id)) next.delete(id);
   else next.add(id);
   return next;
  });
 }
 function toggleAllSessionsFold() {
  setCollapsedSessions(current => {
   const next = new Set(current);
   if (allSessionsCollapsed) {
    for (const id of visibleSessionIds) next.delete(id);
   } else {
    for (const id of visibleSessionIds) next.add(id);
   }
   return next;
  });
 }

 return <>
  {!demo && !review.connectionId && <div className="feedback"><span>选择默认模型后，Knooo 会自动提炼已导入会话。</span><button className="button secondary" onClick={onSettings}>设置默认模型 <ArrowRight size={15}/></button></div>}
  {review.error && <div className="feedback error" role="alert"><span>{review.error}</span><div className="filters">
   <button className="button secondary" onClick={onLogs}>查看回顾日志</button>
   {review.settingsError && <button className="button secondary" onClick={onSettings}>检查模型设置</button>}
  </div></div>}

  {totalTopics > 0 && <div className="review-summary-strip" role="group" aria-label="回顾统计与快速状态筛选">
   <button type="button" className="review-metric" aria-pressed={statusFilter==='all'} onClick={()=>setStatusFilter('all')}>
    <span>活跃项目 / 总条目</span>
    <strong>{projectKeysWithTopics.size} <small>项目 · {totalTopics} 项</small></strong>
   </button>
   <button type="button" className="review-metric" aria-pressed={statusFilter==='done'} onClick={()=>setStatusFilter(current=>current==='done' ? 'all' : 'done')}>
    <span>已完成（验证 / 解决）</span>
    <strong>{doneCount}</strong>
   </button>
   <button type="button" className="review-metric is-open" aria-pressed={statusFilter==='open'} onClick={()=>setStatusFilter(current=>current==='open' ? 'all' : 'open')}>
    <span>待处理</span>
    <strong>{openCount}</strong>
   </button>
   <button type="button" className="review-metric" aria-pressed={statusFilter==='observed'} onClick={()=>setStatusFilter(current=>current==='observed' ? 'all' : 'observed')}>
    <span>已观察</span>
    <strong>{observedCount}</strong>
   </button>
  </div>}

  <div className="section-line review-section">
   <div className="review-heading"><h2>项目回顾</h2>
    <ReviewDropdown label={selected.size ? `筛选项目，已选 ${selected.size} 个` : '筛选项目'} className="review-project-picker"
     trigger={<><Folder size={15}/>{selected.size ? `已选 ${selected.size} 个项目` : '全部项目'}<ChevronDown size={14}/></>}>
     {close=><div className="review-project-options" role="group" aria-label="项目筛选">
      {selected.size>0 && <button className="button text-button" onClick={()=>{close();onSelect(new Map());}}>清除项目筛选</button>}
      {options.size===0 && <p>当前范围没有项目</p>}
      {[...options].sort(([,a],[,b])=>a.project.localeCompare(b.project,'zh-CN') || projectOrigin(a).localeCompare(projectOrigin(b))).map(([key,session])=>
       <label key={key} className="review-project-option"><input type="checkbox" checked={selected.has(key)} onChange={event=>{
        const next = new Map(selected);
        if (event.target.checked) next.set(key,session);
        else next.delete(key);
        onSelect(next);
       }}/><span><strong>{session.project}</strong><small>{projectOrigin(session)}</small></span></label>)}
     </div>}
    </ReviewDropdown>
    <div className="segments" role="group" aria-label="回顾展示模式">
     <button type="button" aria-pressed={viewMode==='focused'} onClick={()=>setViewMode('focused')}>聚焦重点</button>
     <button type="button" aria-pressed={viewMode==='all'} onClick={()=>setViewMode('all')}>全部条目</button>
    </div>
    {totalTopics > 0 && <div className="review-inline-search">
     <Search size={14} aria-hidden="true"/>
     <input value={searchQuery} onChange={e=>setSearchQuery(e.target.value)} placeholder="筛选当前回顾…" aria-label="筛选当前工作回顾"/>
     {searchQuery && <button type="button" className="provider-clear-btn" aria-label="清空回顾筛选" onClick={()=>setSearchQuery('')}><X size={12}/></button>}
    </div>}
    {visibleSessionIds.length > 0 && <button type="button" className="button text-button review-fold-all-btn" onClick={toggleAllSessionsFold}>
     {allSessionsCollapsed ? '展开各会话' : '收起各会话'}
    </button>}
    {hasActiveFilter && <button type="button" className="button text-button" onClick={()=>{setStatusFilter('all');setSearchQuery('');}}>重置筛选</button>}
   </div>
   {visible.length>0 && <div className="review-progress">
    <span role="status">已提炼 {ready} / {visible.length} 条会话{pending>0 ? ` · ${pending} 条处理中或排队` : ''}{cooling>0 ? ` · ${cooling} 条等待自动提炼` : ''}</span>
    {Number.isFinite(nextReviewAt) && <span>下一条会话 · {reviewCountdown(nextReviewAt,now)}</span>}
   </div>}
  </div>

  {(running || pending>0 && !review.error) && <div className="review-flight"><EagleLoading label={running ? '正在提炼会话' : '等待提炼会话'}/></div>}
  {unreviewed.length > 0 && canRetry && <div className="review-unreviewed-box">
   <button type="button" className="review-secondary-toggle" aria-expanded={unreviewedExpanded} onClick={()=>setShowUnreviewed(!unreviewedExpanded)}>
    {unreviewedExpanded ? <ChevronDown size={14}/> : <ChevronRight size={14}/>}
    <span>待提炼会话（{unreviewed.length}）</span>
   </button>
   {unreviewedExpanded && <ul className="review-unreviewed-list">{unreviewed.map(entry=>{
    const {session,state,autoReviewAt} = entry;
    const detail = autoReviewAt ? reviewCountdown(Date.parse(autoReviewAt),now) : state==='skipped' ? '超出自动回顾范围' : state==='error' ? '上次提炼失败' : '';
    return <li key={session.id}>
     <AgentBadge source={session}/>
     <div className="review-unreviewed-copy">
      <strong>{session.title}</strong>
      <small>{[session.project,historyTime(session.updated),detail].filter(Boolean).join(' · ')}</small>
     </div>
     <ReviewRunButton entry={entry} onRetry={onRetry}/>
    </li>})}</ul>}
  </div>}
  {visible.length===0 && <Empty icon={<Folder size={25}/>} title="这个范围没有会话"><p>可以切换时间范围或调整项目和 Agent 筛选。</p></Empty>}
  {groups.size===0 && totalTopics>0 && hasActiveFilter && <Empty icon={<Search size={25}/>} title="当前筛选条件下没有匹配条目"><p>试试切换状态筛选或清空关键词。</p><button type="button" className="button secondary" onClick={()=>{setStatusFilter('all');setSearchQuery('');}}>查看全部条目</button></Empty>}
  {groups.size===0 && totalTopics===0 && ready>0 && unreviewed.length===0 && <Empty icon={<ListChecks size={25}/>} title="没有可回顾的工作问题"><p>这个范围内已处理的会话没有可单独回顾的工作问题。</p></Empty>}
  <div className="projects">{[...groups.entries()].map(([key,entries])=>{
   const project = entries[0].session;
   const expanded = !collapsed.has(key);
   return <section className="project-card" key={key}>
    <h3><button className="project-head review-project-toggle" aria-expanded={expanded} onClick={()=>{
     const next = new Set(collapsed);
     if (expanded) next.add(key);
     else next.delete(key);
     onCollapse(next);
    }}><ProjectIcon/><span className="review-project-name"><strong>{project.project}</strong><small title={project.projectPath}>{projectOrigin(project)}</small></span><span className="project-count-pill"><span>{entries.length} 条会话</span><ChevronDown size={12} strokeWidth={1.9} className="project-chevron" aria-hidden="true"/></span></button></h3>
    {expanded && entries.map(entry=><ReviewSessionBlock key={entry.session.id} entry={entry} collapsed={collapsedSessions.has(entry.session.id)} onToggle={()=>toggleSessionFold(entry.session.id)} focusedMode={viewMode==='focused'} statusFilter={statusFilter} searchTerm={searchTerm} demo={demo} canRetry={canRetry} now={now} onRetry={onRetry} onSaveNote={onSaveNote} open={open}/>)}
   </section>;
  })}</div>

 </>;
}

function SearchResult({hit,sessions,open,searchScope}: {hit:Hit;sessions:Map<string,Session>;open:OpenSession;searchScope:string}) {
 const project = sessions.get(hit.sessionId);
 const sources = new Map<string,Match[]>();
 for (const match of hit.matches) {
  const group=sources.get(match.sessionId) ?? [];
  group.push(match);
  sources.set(match.sessionId,group);
 }
 return <article className="search-hit">
  <div className="result-source"><AgentBadge source={hit}/><span>{hit.project || '未关联项目'}</span>{project && <span>{locationLabel(project)}</span>}</div>
  {project?.projectPath && <div className="result-project-path" title={project.projectPath}>{displayPath(project.projectPath)}</div>}
  <button className="text-button result-title" onClick={()=>open(hit.sessionId)}>{hit.topic.title}<ArrowUpRight size={16}/></button>
  {hit.topic.outcome && <p>{hit.topic.outcome}</p>}
  {[...sources].map(([id,matches])=><SearchTranscript key={id} id={id} session={sessions.get(id)} matches={matches}
   hasMore={hit.moreMatches?.includes(id) ?? false} open={open} searchScope={searchScope}/>)}
 </article>;
}

function SearchTranscript({id,session,matches,hasMore,open,searchScope}:{
 id:string;session?:Session;matches:Match[];hasMore:boolean;open:OpenSession;searchScope:string;
}) {
 const [extra,setExtra] = useState<SearchMatchPage>();
 const [busy,setBusy] = useState(false);
 const [error,setError] = useState('');
 const active = useRef<AbortController | null>(null);
 useEffect(()=>()=>active.current?.abort(),[]);
 async function loadMore() {
  const controller = new AbortController();
  active.current = controller;
  setBusy(true);
  setError('');
  const params = new URLSearchParams(searchScope);
  params.set('session',id);
  params.set('offset',String(matches.length + (extra?.matches.length ?? 0)));
  try {
   const next = await api<SearchMatchPage>(`search/matches?${params}`,undefined,controller.signal);
   if (!controller.signal.aborted) setExtra(previous=>({matches:[...(previous?.matches ?? []),...next.matches],hasMore:next.hasMore}));
  } catch (err) {
   if (!controller.signal.aborted) setError(err instanceof Error ? err.message : String(err));
  } finally {
   if (!controller.signal.aborted) setBusy(false);
  }
 }
 return <div className="result-transcript">
  {session && <button className="text-button transcript-link" onClick={()=>open(id,undefined,'raw')}>
   <FileText size={15}/><span><strong>{locationLabel(session)}</strong><code title={session.path}>{displayPath(session.path)}</code></span><ArrowUpRight size={14}/>
  </button>}
  {[...new Map([...matches,...(extra?.matches ?? [])].filter(m=>m.messageId).map(m=>[m.messageId,m])).values()].map(m=>
   <button className="evidence-link" key={m.messageId} onClick={()=>open(id,m.messageId,'raw',m.evidenceVersion)}><span>{m.snippet}</span><ArrowRight size={15}/></button>)}
  {(extra?.hasMore ?? hasMore) && <button className="button text-button" type="button" disabled={busy} onClick={()=>void loadMore()}>{busy ? '正在读取…' : '加载更多匹配消息'}</button>}
  {error && <p className="feedback error" role="alert">{error}</p>}
 </div>;
}

const scanLabels:Record<string,string> = {
 scanned:'已扫描', missing:'目录不存在', partial:'部分读取', unavailable:'无法读取', stopped:'未运行，未扫描', empty:'未发现发行版',
};

function ScanCoverage({coverage,warnings}: {coverage:Coverage;warnings:string[]}) {
 const grouped = new Map(coverage.environments.map(env=>[locationKey(env),{env,roots:[] as ScanRoot[]}]));
 const indexedAgents = new Set(coverage.roots.filter(root=>root.sessions>0).map(root=>root.agent));
 for (const root of coverage.roots) {
  const key=locationKey(root);
  const group=grouped.get(key);
  if (group) group.roots.push(root);
  else grouped.set(key,{env:{...root,state:'scanned'},roots:[root]});
 }
 return <details className="source-coverage">
  <summary><SquareTerminal size={17}/><strong>扫描来源</strong><span>{[...grouped.values()].map(({env,roots})=>{
   const count=roots.reduce((sum,root)=>sum+root.sessions,0);
   return `${locationLabel(env)} ${env.state==='scanned' ? `${count} 条` : scanLabels[env.state]}`;
  }).join(' / ')}</span></summary>
  <div className="coverage-list">{[...grouped].map(([key,{env,roots}])=><section className="coverage-environment" key={key}>
   <h3>{locationLabel(env)} <span>{env.state==='scanned' && roots.some(r=>r.state==='partial' || r.state==='unavailable') ? '部分读取' : scanLabels[env.state]}</span></h3>
   {env.home && <code className="coverage-home" title={env.home}>{displayPath(env.home)}</code>}
   <ul>{roots.filter(root=>root.sessions>0 || indexedAgents.has(root.agent) && (root.state==='partial' || root.state==='unavailable')).map(root=><li key={`${root.agent}:${root.directory}`}>
    <div><AgentBadge source={root}/><span>{scanLabels[root.state]}{(root.state==='scanned' || root.state==='partial') && ` · ${root.sessions} 条会话`}</span></div>
    <code title={root.directory}>{displayPath(root.directory)}</code>
   </li>)}</ul>
  </section>)}
  {!!warnings.length && <details className="warnings"><summary>{warnings.length} 条扫描提示</summary><ul>{warnings.map((text,i)=><li key={i} title={text}>{displayScanWarning(text)}</li>)}</ul></details>}
  </div>
 </details>;
}

function SessionPaths({session}: {session:Session}) {
 const [feedback,setFeedback]=useState('');
 async function copy(label:string,value:string) {
  try {
   await navigator.clipboard.writeText(value);
   setFeedback(`已复制${label}`);
  } catch {
   setFeedback('无法写入剪贴板，可选中路径手动复制。');
  }
 }
 const paths = [
  ['项目目录',session.projectPath],
  ...(session.cwd && session.cwd!==session.projectPath ? [['工作目录',session.cwd]] : []),
  ['Agent 日志目录',session.sourceRoot],
  ['会话文件',session.path],
 ];
 return <>
  <dl>{paths.filter(([,path])=>path).map(([label,path])=><div key={label}>
   <dt>{label}</dt>
   <dd><code title={path}>{displayPath(path)}</code><button className="icon-button" aria-label={`复制${label}`} title={`复制${label}`} onClick={()=>void copy(label,path)}><Copy size={15}/></button></dd>
  </div>)}</dl>
  {feedback && <p role="status">{feedback}</p>}
 </>;
}

type HistoryMatches = Record<string,Match>;
type HistoryOpen = (id:string, anchor?:string)=>void;
type HistorySelection = {picked:Set<string>; toggle:(id:string)=>void; busy:boolean};

function SessionLibrary({listing,scanning,selected,open,reload,onError,onNotice}:{
 listing:Listing;scanning:boolean;selected?:string;open:HistoryOpen;reload:()=>Promise<Listing>;onError:(error:unknown)=>void;onNotice:(message:string)=>void;
}) {
 const [storedAgents,setAgents]=useState(()=>(localStorage.getItem('knooo-history-agent') ?? '').split(',').filter(Boolean));
 const [location,setLocation]=useState('');
 const [selectedProjects,setSelectedProjects]=useState<Set<string>>(()=>new Set());
 const [projectFilterQuery,setProjectFilterQuery]=useState('');
 const [query,setQuery]=useState('');
 const [search,setSearch]=useState<{key:string;matches:HistoryMatches;error?:string}>();
 const [archived,setArchived]=useState(false);
 const [picking,setPicking]=useState(false);
 const [picked,setPicked]=useState(new Set<string>());
 const [busy,setBusy]=useState(false);
 const scope=listing.sessions.filter(s=>!!s.archived===archived);
 const locations=[...new Map(scope.map(s=>[locationKey(s),locationLabel(s)]))];
 const activeLocation=locations.some(([id])=>id===location) ? location : '';
 const locationSessions=activeLocation ? scope.filter(s=>activeLocation===locationKey(s)) : scope;

 const {rootProjectKeys, projectOptions}=useMemo(()=>{
  const byId=new Map(locationSessions.map(s=>[s.id,s]));
  const rootKeys=new Map<string,string>();
  const options=new Map<string,{session:Session;count:number;latest:number}>();
  for (const session of locationSessions) {
   let root=session;
   let parent=byId.get(root.parentId ?? '');
   const seen=new Set([session.id]);
   while (parent && !seen.has(parent.id) && seen.size <= 8) {
    root=parent;seen.add(parent.id);parent=byId.get(parent.parentId ?? '');
   }
   if (parent) root=session;
   const pKey=projectKey(root);
   rootKeys.set(session.id,pKey);
   const updatedAt=Date.parse(session.updated || session.start) || 0;
   const existing=options.get(pKey);
   if (existing) {
    existing.count++;
    if (updatedAt > existing.latest) existing.latest=updatedAt;
   } else {
    options.set(pKey,{session:root,count:1,latest:updatedAt});
   }
  }
  return {rootProjectKeys:rootKeys,projectOptions:options};
 },[locationSessions]);

 const activeProjects=useMemo(()=>{
  const next=new Set<string>();
  for (const key of selectedProjects) {
   if (projectOptions.has(key)) next.add(key);
  }
  return next;
 },[selectedProjects,projectOptions]);

 const projectSessions=activeProjects.size
  ? locationSessions.filter(s=>activeProjects.has(rootProjectKeys.get(s.id) ?? projectKey(s)))
  : locationSessions;
 const availableAgents=countAgents(projectSessions);
 const selectedAgents=storedAgents.filter(agent=>availableAgents.has(agent));
 const term=query.trim();
 const key=agentQuery({q:term},selectedAgents);
 const matches=term && search?.key===key ? search.matches : undefined;
 const searching=!!term && search?.key!==key;
 const searchError=term && search?.key===key ? search.error : undefined;
 const sessions=selectedAgents.length ? projectSessions.filter(s=>selectedAgents.includes(agentKey(s))) : projectSessions;
 const matchCount=matches ? sessions.filter(s=>Object.hasOwn(matches,s.id)).length : sessions.length;
 const loading=scanning && (!matchCount || searching);
 const selection=picking ? {picked,toggle(id:string){setPicked(current=>{
  const next=new Set(current);
  if (next.has(id)) next.delete(id); else next.add(id);
  return next;
 });},busy} : undefined;

 const sortedProjectOptions=useMemo(()=>{
  const q=projectFilterQuery.trim().toLowerCase();
  const list=[...projectOptions.entries()].sort(([,a],[,b])=>
   b.latest - a.latest || (a.session.project || '未关联项目').localeCompare(b.session.project || '未关联项目','zh-CN')
  );
  if (!q) return list;
  return list.filter(([,item])=>
   (item.session.project || '未关联项目').toLowerCase().includes(q) ||
   projectOrigin(item.session).toLowerCase().includes(q)
  );
 },[projectOptions,projectFilterQuery]);

 const activeProjectLabel=useMemo(()=>{
  if (activeProjects.size === 0) return '全部项目';
  if (activeProjects.size === 1) {
   const onlyKey=[...activeProjects][0];
   return projectOptions.get(onlyKey)?.session.project || '未关联项目';
  }
  return `已选 ${activeProjects.size} 个项目`;
 },[activeProjects,projectOptions]);

 useEffect(()=>{
  if (!term || scanning) return;
  const controller=new AbortController();
  const timer=setTimeout(async ()=>{
   try {
    const matches=await api<HistoryMatches>(`history/search?${key}`,undefined,controller.signal);
    if (!controller.signal.aborted) setSearch({key,matches});
   } catch (error) {
    if (!controller.signal.aborted) setSearch({key,matches:{},error:error instanceof Error ? error.message : '无法搜索会话'});
   }
  },220);
  return ()=>{clearTimeout(timer);controller.abort();};
 },[term,key,scanning,listing.sessions]);
 function resetSelection() { setPicked(new Set());setPicking(false); }
 function selectAgents(agents:string[]) {
  setAgents(agents);
  localStorage.setItem('knooo-history-agent',agents.join(','));
  resetSelection();
 }
 function toggleProject(pKey:string, checked:boolean) {
  setSelectedProjects(current=>{
   const next=new Set(current);
   if (checked) next.add(pKey);
   else next.delete(pKey);
   return next;
  });
  resetSelection();
 }
 function clearProjects() {
  setSelectedProjects(new Set());
  setProjectFilterQuery('');
  resetSelection();
 }
 async function archivePicked() {
  const ids=sessions.filter(s=>picked.has(s.id)).map(s=>s.id);
  if (!ids.length) return;
  setBusy(true);
  try {
   await api('history/archive',{ids,archived:!archived});
   await reload();
   resetSelection();
   onNotice(`已${archived ? '恢复' : '归档'} ${ids.length} 条会话。`);
  } catch (error) { onError(error); }
  finally { setBusy(false); }
 }
 const projectKeyToken=[...activeProjects].sort().join(',');
 return <>
  <div className="history-toolbar">
   <AgentPicker counts={availableAgents} selected={selectedAgents} onChange={selectAgents} disabled={busy}/>
   <div className="history-actions">
    <label className="select"><SquareTerminal size={16}/><select aria-label="运行环境" value={activeLocation} disabled={busy} onChange={e=>{setLocation(e.target.value);resetSelection();}}><option value="">全部环境</option>{locations.map(([id,label])=><option key={id} value={id}>{label}</option>)}</select><ChevronDown size={13}/></label>
    <ReviewDropdown label={activeProjects.size ? `筛选项目，已选 ${activeProjects.size} 个` : '筛选项目'}
     className={`review-project-picker history-project-picker ${activeProjects.size ? 'has-selection' : ''}`}
     trigger={<><Folder size={15}/><span className="history-project-trigger-label">{activeProjectLabel}</span><ChevronDown size={13}/></>}>
     {close=><div className="review-project-options" role="group" aria-label="项目筛选">
      {projectOptions.size > 5 && <div className="history-project-filter-search">
       <Search size={13} aria-hidden="true"/>
       <input value={projectFilterQuery} onChange={e=>setProjectFilterQuery(e.target.value)} placeholder="筛选项目名称或路径…" aria-label="筛选项目列表"/>
       {projectFilterQuery && <button type="button" className="provider-clear-btn" aria-label="清空项目搜索" onClick={()=>setProjectFilterQuery('')}><X size={12}/></button>}
      </div>}
      {activeProjects.size>0 && <button type="button" className="button text-button" onClick={()=>{close();clearProjects();}}>清除项目筛选</button>}
      {sortedProjectOptions.length===0 && <p>{projectFilterQuery.trim() ? '没有匹配的项目' : '当前范围没有项目'}</p>}
      {sortedProjectOptions.map(([pKey,{session,count}])=>
       <label key={pKey} className="review-project-option">
        <input type="checkbox" checked={activeProjects.has(pKey)} onChange={event=>toggleProject(pKey,event.target.checked)}/>
        <span><strong>{session.project || '未关联项目'}</strong><small>{projectOrigin(session)}</small></span>
        <em className="history-project-option-count">{count}</em>
       </label>)}
     </div>}
    </ReviewDropdown>
    <label className="history-search"><Search size={16}/><input type="search" value={query} disabled={busy} aria-label="搜索会话" placeholder="搜索标题、路径或原文" onChange={e=>{setQuery(e.target.value);setPicked(new Set());}}/></label>
    <button className="button secondary" aria-pressed={picking} disabled={busy || (!picking && !matchCount)} onClick={()=>{setPicking(!picking);setPicked(new Set());}}><ListChecks size={16}/>{picking ? '取消选择' : '选择'}</button>
    <button className="button secondary" aria-pressed={archived} disabled={busy} onClick={()=>{setArchived(!archived);setLocation('');clearProjects();}}><Archive size={16}/>{archived ? '返回会话' : '已归档'}</button>
   </div>
  </div>
  <div className="history-status">
   <div className="history-status-summary">
    <span className="muted" role="status">{loading || searching ? '' : searchError ? '搜索失败' : `${matchCount} 条${archived ? '已归档' : ''}会话`}</span>
    {activeProjects.size > 0 && <button type="button" className="button text-button history-clear-projects" onClick={clearProjects}>清除项目筛选</button>}
   </div>
   {picking && <button className="button primary" disabled={busy || !picked.size} onClick={()=>void archivePicked()}>{busy ? <EagleMark flying/> : archived ? <ArchiveRestore size={16}/> : <Archive size={16}/>} {busy ? archived ? '正在恢复…' : '正在归档…' : `${archived ? '恢复' : '归档'}所选（${picked.size}）`}</button>}
  </div>
  {listing.coverage && <ScanCoverage coverage={listing.coverage} warnings={listing.warnings ?? []}/>}
  {loading ? <EagleLoading label="正在发现本机会话"/> :
   searching ? <EagleLoading label="正在搜索会话"/> :
   searchError ? <p className="feedback error" role="alert">{searchError}</p> :
   matchCount>0 ? <SessionHistory key={`${archived}:${activeLocation}:${projectKeyToken}:${key}`} sessions={sessions} environments={listing.coverage?.environments} selected={selected} open={open} matches={matches} selection={selection} expandAll={activeProjects.size > 0}/> :
   <Empty icon={<MessagesSquare size={25}/>} title={term ? '没有匹配的会话' : archived ? '没有已归档的会话' : '当前视图没有会话'}>
    <p>{term ? '试试缩短关键字。' : selectedAgents.length || activeLocation || activeProjects.size ? '切换 Agent、项目或运行环境查看其他来源。' : archived ? '归档的会话可以在这里恢复。' : 'Agent 历史会自动出现在这里。'}</p>
   </Empty>}
 </>;
}

function SessionHistory({sessions, environments, selected, open, matches, selection, expandAll=false}: {sessions:Session[]; environments?:ScanEnvironment[]; selected?:string; open:HistoryOpen;matches?:HistoryMatches;selection?:HistorySelection;expandAll?:boolean}) {
 const {groups, children} = useMemo(()=>{
  const byId = new Map(sessions.map(s=>[s.id,s]));
  const visible=new Set<string>();
  if (matches) {
   for (const session of sessions) {
    if (!Object.hasOwn(matches,session.id)) continue;
    let item:Session|undefined=session;
    while (item && !visible.has(item.id)) {
     visible.add(item.id);
     item=byId.get(item.parentId ?? '');
    }
   }
  }
  const children = new Map<string,Session[]>();
  const groups = new Map<string,{origin:Session; latest:Session; roots:Session[]; count:number}>();
  for (const session of sessions) {
   if (matches && !visible.has(session.id)) continue;
   let root = session;
   let parent = byId.get(root.parentId ?? '');
   const seen = new Set([session.id]);
   while (parent && !seen.has(parent.id) && seen.size <= 8) {
    root=parent;seen.add(parent.id);parent=byId.get(parent.parentId ?? '');
   }
   if (parent) root=session; // Keep cycles and excessively deep chains visible at the top.
   if (root!==session) {
    const siblings = children.get(session.parentId!) ?? [];
    siblings.push(session);
    children.set(session.parentId!,siblings);
   }
   const key = projectKey(root);
   const group = groups.get(key) ?? {origin:root,latest:session,roots:[],count:0};
   group.count++;
   if (root===session) group.roots.push(session);
   groups.set(key,group);
  }
  for (const siblings of children.values()) {
   siblings.sort((a,b)=>Date.parse(a.start || a.updated)-Date.parse(b.start || b.updated));
  }
  return {groups,children};
 },[sessions,matches]);
 return <div className="projects">{[...groups].map(([path,group],index)=>
  <HistoryProject key={path} group={group} home={environments?.find(env=>locationKey(env)===locationKey(group.origin))?.home} defaultOpen={!!matches || expandAll || index===0}>
   {group.roots.map(session=><HistoryBranch key={session.id} session={session} childrenById={children} selected={selected} open={open} matches={matches} selection={selection}/>)}
  </HistoryProject>)}</div>;
}

function HistoryProject({group, home, defaultOpen, children}:{
 group:{origin:Session;latest:Session;count:number}; home?:string; defaultOpen:boolean; children:ReactNode;
}) {
 const [expanded,setExpanded] = useState(defaultOpen);
 const {project,projectPath,wsl}=group.origin;
 const title=project || '未关联项目';
 const path=wsl ? homeRelativePath(projectPath,home ?? '') : displayPath(projectPath);
 const location=wsl || locationLabel(group.origin);
 return <section className="project-card history-project">
  <button className="project-head" aria-expanded={expanded} onClick={()=>setExpanded(!expanded)}>
   <span className="tree-chevron" aria-hidden="true"><ChevronRight size={12} strokeWidth={2.2}/></span>
   <ProjectIcon/>
   <span className="history-project-copy"><strong title={title}>{title}</strong><small title={projectOrigin(group.origin)}>{location}{path && ` · ${path}`}</small></span>
   <time className="history-project-time" dateTime={group.latest.updated}>{historyTime(group.latest.updated)}</time>
   <span className="project-count-tag">{group.count} 条会话</span>
  </button>
  {expanded && <div className="history-project-body">{children}</div>}
 </section>;
}

function HistoryBranch({session, childrenById, selected, open, matches, selection, depth=0}:{
 session:Session; childrenById:Map<string,Session[]>; selected?:string; open:HistoryOpen;matches?:HistoryMatches;selection?:HistorySelection;depth?:number;
}) {
 const [expanded,setExpanded] = useState(!!matches);
 const [showSource,setShowSource] = useState(false);
 const sourceId=useId();
 const children = childrenById.get(session.id);
 const match=matches && Object.hasOwn(matches,session.id) ? matches[session.id] : undefined;
 const isChild = depth > 0;
 return <div className={`history-branch ${isChild ? 'is-child' : ''}`}>
  <div className={`history-entry ${selection ? 'picking' : ''} ${children ? 'has-children' : ''}`}>
   {!isChild && <div className="history-tree-gutter">
    {children ? <button type="button" className="history-fold" aria-expanded={expanded} aria-label={`${expanded ? '收起' : '展开'} ${session.title} 的 ${children.length} 条子会话`} onClick={()=>setExpanded(!expanded)}>
     <ChevronRight size={11} strokeWidth={2.3}/>
    </button> : <span className="history-leaf-dot" aria-hidden="true"/>}
   </div>}
   {isChild && children && <div className="history-tree-gutter is-inline">
    <button type="button" className="history-fold" aria-expanded={expanded} aria-label={`${expanded ? '收起' : '展开'} ${session.title} 的 ${children.length} 条子会话`} onClick={()=>setExpanded(!expanded)}>
     <ChevronRight size={11} strokeWidth={2.3}/>
    </button>
   </div>}
   {selection && <input type="checkbox" className="history-check" aria-label={`选择 ${session.title}`} checked={selection.picked.has(session.id)} disabled={selection.busy} onChange={()=>selection.toggle(session.id)}/>}
   <div className={`session-row ${selected===session.id ? 'selected' : ''}`}>
    <span className={`session-icon ${isChild ? 'is-child' : ''}`} aria-hidden="true">{isChild ? <ListTree size={13} strokeWidth={2.1}/> : <MessagesSquare size={14} strokeWidth={2}/>}</span>
    <div className="session-copy">
     <button className="session-open" aria-current={selected===session.id || undefined} title={session.title} onClick={()=>open(session.id,match?.messageId)}>{session.title}</button>
     <div className="session-labels">
      <button className="history-source-toggle" aria-label={`会话来源 ${locationLabel(session)} · ${session.title}`} aria-expanded={showSource} aria-controls={showSource ? sourceId : undefined} onClick={()=>setShowSource(!showSource)}>
       <SquareTerminal size={11.5}/><span>{locationLabel(session)}</span><ChevronDown size={10.5} strokeWidth={1.9}/>
      </button>
      <AgentBadge source={session}/>
      {children && <button type="button" className="history-children-badge" aria-expanded={expanded} onClick={()=>setExpanded(!expanded)}>
       {children.length} 条子会话
      </button>}
     </div>
     {match?.snippet && <small>{match.snippet}</small>}
    </div>
    <div className="session-stats">
     <time dateTime={session.updated}>{historyTime(session.updated)}</time>
     <span>{session.count} 条消息</span>
    </div>
   </div>
  </div>
  {showSource && <div className="history-source-panel session-source" id={sourceId}><SessionPaths session={session}/></div>}
  {expanded && children && <div className="child-session">{children.map(child=><HistoryBranch key={child.id} session={child} childrenById={childrenById} selected={selected} open={open} matches={matches} selection={selection} depth={depth+1}/>)}</div>}
 </div>;
}

function Reader({focus,setFocus,collapsed,close,profile,model,modelPicker,panelGrip,demo,childrenSessions,parent,open}: {
 focus:ReaderFocus;setFocus:(f:ReaderFocus)=>void;collapsed:boolean;close:()=>void;profile?:Profile;model:string;
 modelPicker:(disabled:boolean)=>ReactNode;panelGrip:ReactNode;demo:boolean;childrenSessions:Session[];parent?:Session;
 open:(id:string,anchor?:string)=>void;
}) {
 const [detail,setDetail]=useState<Detail>();
 const [error,setError]=useState('');
 const [busy,setBusy]=useState(false);
 const [saved,setSaved]=useState<string[]>([]);
 const [saving,setSaving]=useState<string>();
 const [confirm,setConfirm]=useState<DistillPlan>();
 const scroller=useRef<HTMLDivElement>(null);
 const [checkedReference,setCheckedReference] = useState('');
 const reference = focus.evidenceVersion && focus.anchor ? `${focus.anchor}:${focus.evidenceVersion}` : '';
 useEffect(()=>{
  const controller = new AbortController();
  const params = new URLSearchParams({id:focus.id});
  if (focus.anchor && focus.evidenceVersion) {
   params.set('anchor',focus.anchor);
   params.set('evidenceVersion',focus.evidenceVersion);
  }
  api<Detail>(`session?${params}`,undefined,controller.signal)
   .then(next=>{if(!controller.signal.aborted) {setDetail(next);setCheckedReference(reference);}})
   .catch(err=>{if(!controller.signal.aborted) setError(err.message);});
  return ()=>controller.abort();
 },[focus.id,focus.anchor,focus.evidenceVersion,reference]);
 const checked = !reference || reference===checkedReference;
 const activeAnchor = checked && (!reference || !detail?.staleAnchor) ? focus.anchor : undefined;
 useEffect(()=>{
  if (focus.tab==='raw' && activeAnchor && detail) document.getElementById(`message-${activeAnchor}`)?.scrollIntoView({block:'nearest'});
  else if (scroller.current) scroller.current.scrollTop=0;
 },[focus.tab,activeAnchor,detail]);
 useEffect(()=>{setConfirm(undefined);},[profile?.id,model]);
 function distillRequest() {
  return {id:focus.id,connectionId:demo ? '' : profile?.id,model:demo ? '' : model.trim()};
 }
 async function preview() {
  setError('');setBusy(true);
  try {setConfirm(await api<DistillPlan>('distill',{...distillRequest(),preview:true}));}
  catch(e){setError((e as Error).message);} finally {setBusy(false);}
 }
 async function generate() {
  setConfirm(undefined);setError('');setBusy(true);
  try { const topics=await api<Topic[]>('distill',{...distillRequest(),confirmation:confirm?.confirmation});setDetail(d=>d ? {...d,topics,generated:true} : d); }
  catch(e){setError((e as Error).message);} finally {setBusy(false);}
 }
 async function save(topic:Topic) {
  setError('');setSaving(topic.title);
  try{await api('notes',{sessionId:focus.id,topic});setSaved(previous=>[...previous,topic.title]);}
  catch(e){setError((e as Error).message);} finally {setSaving(undefined);}
 }
 const size=detail?.session.messages?.length ?? 0;
 return <aside data-collapsed={collapsed} className="reader" id="session-reader" aria-label="会话阅读器">{panelGrip}<div className="reader-top"><span><MessagesSquare size={17}/> 会话阅读器</span><button className="icon-button" onClick={close} aria-label="收起会话阅读器" title="收起阅读器"><PanelRightClose size={18}/></button></div>{detail && <><div className="reader-intro"><div className="result-source"><AgentBadge source={detail.session}/><span>{detail.session.project}</span><span>{locationLabel(detail.session)}</span></div><h2>{detail.session.title}</h2><div className="reader-meta">{size} 条消息 · {formatTime(detail.session.updated)}</div></div><div className="reader-tabs"><button aria-pressed={focus.tab==='topic'} onClick={()=>setFocus({id:focus.id,tab:'topic'})}><ScanText size={15}/> 主题</button><button aria-pressed={focus.tab==='raw'} onClick={()=>setFocus({id:focus.id,tab:'raw'})}><FileText size={15}/> 原文</button></div></>}
 <div className="reader-body" ref={scroller}>{error && <p className="feedback error" role="alert">{error}</p>}{!detail && !error && <EagleLoading label="正在读取会话"/>}{detail && <details className="source-coverage session-source" open={focus.tab==='raw'}><summary><Folder size={16}/><strong>会话来源</strong><span>{locationLabel(detail.session)}</span></summary><SessionPaths session={detail.session}/></details>}
 {detail && (focus.tab==='raw' ? <>{focus.anchor && checked && (!activeAnchor || !detail.session.messages?.some(m=>m.id===activeAnchor)) && <p className="feedback error" role="status">引用的消息已变更，可浏览当前原文。</p>}{detail.session.messages?.map(m=><article id={`message-${m.id}`} key={m.id} className={`message ${activeAnchor===m.id ? 'highlighted' : ''}`}><div><strong>{m.role==='user' ? '用户' : m.role==='assistant' ? agentName(detail.session) : m.role}</strong><span>{detail.session.agent==='antigravity' ? `步骤 ${m.line}` : `第 ${m.line} 行`} · {formatTime(m.timestamp)}</span></div><p>{m.text}</p></article>)}</>:<>{modelPicker(busy || !!confirm)}<div className="extract-bar"><span className="muted">{detail.generated ? '已保存的主题' : '本地摘录'}</span><button className="button primary" disabled={busy || !!confirm || size===0 || !!profile && (!profile.ready || !model.trim())} onClick={()=>{if(!profile || demo) void generate();else void preview();}}>{busy ? <EagleMark flying/> : <ScanText size={15}/>} {busy ? '正在处理会话…' : detail.generated ? '重新提炼' : '提炼会话'}</button></div>{confirm && <div className="confirm"><p>通过「{profile?.name}」向 <strong>{model}</strong> 发送 {confirm.included} / {confirm.total} 条消息。</p>
 <p>预计输入 {confirm.estimatedInput.toLocaleString()} / {confirm.inputBudget.toLocaleString()} token，输出最多 {(confirm.totalOutputBudget ?? confirm.outputBudget).toLocaleString()} token{confirm.thinkingBudget ? `（正文 ${confirm.outputBudget.toLocaleString()}，思考 ${confirm.thinkingBudget.toLocaleString()}）` : ''}。</p>
 {(confirm.included<confirm.total || confirm.shortened) && <p>超出预算时优先保留近期消息。{confirm.shortened && '最新长消息只发送开头和结尾。'}</p>}
 {profile?.preset==='gateway' && <p>本地网关会按其配置处理请求，可能转发给云模型供应商。</p>}
 <button className="button secondary" onClick={()=>setConfirm(undefined)}>取消</button> <button className="button primary" onClick={()=>void generate()}>发送并提炼</button></div>}{(detail.topics ?? []).map((t,i)=><section className="topic" key={`${t.title}-${i}`}><div className="status-label">{({verified:'已验证',resolved:'已解决',open:'待处理',observed:'原文摘录'} as Record<string,string>)[t.status] ?? t.status}</div><h3>{t.title}</h3>{t.problem && <p>{t.problem}</p>}{t.outcome && <p>{t.outcome}</p>}{t.lesson && <p><strong>可复用经验</strong><br/>{t.lesson}</p>}{t.exercise && <p><strong>学习练习</strong><br/>{t.exercise}</p>}{t.evidenceStale ? <p className="feedback error">原文依据已变更或需要核对，可重新提炼。</p> : <div className="topic-evidence">{(t.evidence ?? []).map((id,j)=><button key={id} className="button secondary" onClick={()=>setFocus({id:focus.id,anchor:id,tab:'raw',evidenceVersion:t.evidenceVersions?.[id]})}>查看依据 {j+1}<ArrowUpRight size={14}/></button>)}</div>}{!t.evidenceStale && t.evidence?.length>0 && <button className="button text-button" disabled={saving!==undefined || saved.includes(t.title)} onClick={()=>void save(t)}>{saving===t.title ? <EagleMark flying/> : saved.includes(t.title) ? <Check size={15}/> : <Plus size={15}/>} {saving===t.title ? '正在保存…' : saved.includes(t.title) ? '已存入知识库' : '存入知识库'}</button>}</section>)}</>)}
 {parent && <button className="button secondary" onClick={()=>open(parent.id)}><ArrowLeft size={15}/> 父会话</button>}{childrenSessions.length>0 && <section className="children"><h3>子会话</h3>{childrenSessions.map(s=><button key={s.id} className="evidence-link" onClick={()=>open(s.id)}><span>{s.title}</span><ChevronRight size={15}/></button>)}</section>}</div></aside>;
}

const reportPresets = [{id:'daily',label:'日报'},{id:'weekly',label:'周报'},{id:'monthly',label:'月报'}] as const;
type ReportKind = typeof reportPresets[number]['id'] | 'custom';
type SavedReport = {
 report:WorkReport|null;
 reportRevision:number;
 draft:WorkReport|null;
 draftRevision:number;
};
type ReportView = {kind:ReportKind;range:DateInterval;projectKeyValue:string};
type PendingReportDraft = {draft:WorkReport;revision:number;savedRevision?:number};
const reportInFlight = new Set<string>();
const savingReportDrafts = new Set<string>();
const pendingReportDrafts = new Map<string,PendingReportDraft>();
const reportDraftErrors = new Map<string,string>();
const reportListeners = new Set<(scope:string)=>void>();
let reportView:ReportView | undefined;

function hasLegacyReportCache() {
 try {
  for (let index=0;index<localStorage.length;index++) {
   const key = localStorage.key(index);
   if (key==='knooo-reports-v1:local' || key==='knooo-reports-v1:demo') return true;
  }
 } catch { /* Storage can be blocked by browser settings. */ }
 return false;
}

function warnUnsavedReport(event:BeforeUnloadEvent) {
 event.preventDefault();
 event.returnValue = true;
}
function updatePendingReport(scope:string, pending?:PendingReportDraft) {
 if (pending) pendingReportDrafts.set(scope,pending);
 else pendingReportDrafts.delete(scope);
 if ([...pendingReportDrafts.values()].some(entry=>entry.savedRevision===undefined)) {
  window.addEventListener('beforeunload',warnUnsavedReport);
 } else {
  window.removeEventListener('beforeunload',warnUnsavedReport);
 }
}
function notifyReportListeners(scope:string) {
 for (const listener of reportListeners) listener(scope);
}

const twoDigits = (value:number)=>String(value).padStart(2,'0');
function localTimestamp(date:Date) {
 const offset = -date.getTimezoneOffset();
 return `${dateValue(date)}T${twoDigits(date.getHours())}:${twoDigits(date.getMinutes())}:00${offset<0 ? '-' : '+'}${twoDigits(Math.floor(Math.abs(offset)/60))}:${twoDigits(Math.abs(offset)%60)}`;
}
function Reports({listing,agentCounts,selectedAgents,onAgentsChange,profiles,onOpen,fail}:{
 listing:Listing;agentCounts:ReadonlyMap<string,number>;selectedAgents:readonly string[];onAgentsChange:(agents:string[])=>void;profiles:Profile[];
 onOpen:OpenSession;fail:(e:unknown)=>void;
}) {
 const [view,setView] = useState<ReportView>(()=>{
  const today = dateValue(dayStart());
  return reportView ?? {kind:'daily',range:{from:today,to:today},projectKeyValue:''};
 });
 const {kind,range,projectKeyValue} = view;
 const [projectResult,setProjectResult] = useState<{query:string;projects:ProjectSource[];error?:string}>();
 const [reportResult,setReportResult] = useState<{scope:string;document?:SavedReport;error?:string}>();
 const [legacyCache] = useState(hasLegacyReportCache);
 const [,rerender] = useState(0);
 const [connectionId,setConnectionId] = useState('');
 const connection = profiles.find(profile=>profile.id===connectionId && profile.ready && profile.model);
 const {from,to}=range;
 const end = new Date(`${to}T00:00:00`);
 end.setDate(end.getDate()+1);
 end.setHours(0,0,0,0);
 const projectQuery = agentQuery({from:localTimestamp(new Date(`${from}T00:00:00`)),to:localTimestamp(end)},selectedAgents);
 const currentProjects = projectResult?.query===projectQuery ? projectResult : undefined;
 const selected = currentProjects?.projects.find(item=>projectKey(item)===projectKeyValue);
 const groups = new Map<string,{label:string;projects:ProjectSource[]}>();
 for (const item of currentProjects?.projects ?? []) {
  const key=locationKey(item);
  if (!groups.has(key)) groups.set(key,{label:locationLabel(item),projects:[]});
  groups.get(key)?.projects.push(item);
 }
 const args = new URLSearchParams(projectQuery);
 args.set('kind',kind);
 if (selected) {
  args.set('project',selected.projectPath);
  args.set('location',locationKey(selected));
 }
 const scope = !projectKeyValue || selected ? args.toString() : '';
 const busy = reportInFlight.has(scope);
 const saving = savingReportDrafts.has(scope);
 const currentResult = reportResult?.scope===scope ? reportResult : undefined;
 const saved = currentResult?.document;
 const pending = pendingReportDrafts.get(scope);
 const dirty = !!pending && pending.savedRevision===undefined;
 function updateReportView(patch:Partial<ReportView>) {
  reportView = {...view,...patch};
  setView(reportView);
 }
 useEffect(()=>{
  const controller = new AbortController();
  api<{connectionId:string}>('review-settings',undefined,controller.signal).then(({connectionId})=>{
   if (!controller.signal.aborted) setConnectionId(connectionId);
  }).catch(()=>{});
  return ()=>controller.abort();
 },[]);
 useEffect(()=>{
  const controller = new AbortController();
  api<ProjectSource[]>(`report/projects?${projectQuery}`,undefined,controller.signal).then(projects=>{
   if (controller.signal.aborted) return;
   projects.sort((a,b)=>a.project.localeCompare(b.project,'zh-CN') || a.projectPath.localeCompare(b.projectPath));
   setProjectResult({query:projectQuery,projects});
   const currentProject = reportView?.projectKeyValue;
   if (reportView && currentProject && !projects.some(item=>projectKey(item)===currentProject)) {
    reportView = {...reportView,projectKeyValue:''};
    setView(reportView);
   }
  }).catch(err=>{
   if (!controller.signal.aborted) setProjectResult({query:projectQuery,projects:[],error:err instanceof Error ? err.message : String(err)});
  });
  return ()=>controller.abort();
 },[projectQuery,listing]);
 useEffect(()=>{
  if (!scope) return;
  let controller:AbortController | undefined;
  function load(target:string) {
   if (target!==scope) return;
   controller?.abort();
   const request = new AbortController();
   controller = request;
   api<SavedReport>(`report/document?${scope}`,undefined,request.signal).then(document=>{
    if (request.signal.aborted) return;
    const entry = pendingReportDrafts.get(scope);
    if (entry?.savedRevision!==undefined && entry.savedRevision<=document.draftRevision) updatePendingReport(scope);
    setReportResult({scope,document});
   }).catch(err=>{
    if (!request.signal.aborted) setReportResult(current=>({
     scope,document:current?.scope===scope ? current.document : undefined,
     error:err instanceof Error ? err.message : String(err),
    }));
   });
  }
  reportListeners.add(load);
  load(scope);
  return ()=>{
   controller?.abort();
   reportListeners.delete(load);
  };
 },[scope]);
 function selectPreset(next:typeof reportPresets[number]['id']) {
  const today = dayStart();
  const start = next==='weekly' ? dayStart(-((today.getDay()+6)%7)) : next==='monthly' ? new Date(today.getFullYear(),today.getMonth(),1) : today;
  updateReportView({kind:next,range:{from:dateValue(start),to:dateValue(today)}});
 }
 async function generate(event:FormEvent) {
  event.preventDefault();
  if (!scope || reportInFlight.has(scope) || !currentProjects || currentProjects.error) return;
  reportInFlight.add(scope);
  rerender(count=>count+1);
  try {
   await api<WorkReport>(`report?${scope}`,{connectionId:connection?.id ?? ''});
  } catch(err) {
   fail(err);
  } finally {
   reportInFlight.delete(scope);
   notifyReportListeners(scope);
  }
 }
 function editDraft(draft:WorkReport) {
  if (!saved || savingReportDrafts.has(scope)) return;
  updatePendingReport(scope,{draft,revision:pending?.savedRevision ?? pending?.revision ?? saved.draftRevision});
  reportDraftErrors.delete(scope);
  rerender(count=>count+1);
 }
 function discardPendingDraft() {
  updatePendingReport(scope);
  reportDraftErrors.delete(scope);
  rerender(count=>count+1);
 }
 async function saveDraft(useGenerated:boolean) {
  if (!saved || savingReportDrafts.has(scope) || (!useGenerated && !pending)) return;
  reportDraftErrors.delete(scope);
  savingReportDrafts.add(scope);
  rerender(count=>count+1);
  try {
   const result = await api<{draftRevision:number}>(`report/document?${scope}`,{
    draft:useGenerated ? null : pending?.draft,
    draftRevision:useGenerated ? saved.draftRevision : pending?.revision,
    reportRevision:saved.reportRevision,
   });
   if (useGenerated) updatePendingReport(scope);
   else if (pending) updatePendingReport(scope,{...pending,savedRevision:result.draftRevision});
  } catch(err) {
   reportDraftErrors.set(scope,err instanceof Error ? err.message : String(err));
  } finally {
   savingReportDrafts.delete(scope);
   notifyReportListeners(scope);
   rerender(count=>count+1);
  }
 }
 return <><form className="settings-box report-box" onSubmit={generate}>
  <div className="report-toolbar">
   <div className="review-time">
    <div className="segments" role="group" aria-label="报告类型">{reportPresets.map(({id,label})=><button type="button" key={id} aria-pressed={kind===id} onClick={()=>selectPreset(id)}>{label}</button>)}</div>
    <DateRangePicker range={range} active={kind==='custom'} onChange={next=>updateReportView({kind:'custom',range:next})}/>
   </div>
   <label className="select report-project"><Folder size={16}/><select aria-label="报告项目" title={selected?.projectPath ?? '全部项目'} value={selected ? projectKeyValue : ''} disabled={!groups.size} onChange={event=>updateReportView({projectKeyValue:event.target.value})}>
    <option value="">{!currentProjects ? '正在读取项目…' : currentProjects.error ? '项目读取失败' : groups.size ? '全部项目' : '期间无项目'}</option>
    {[...groups].sort((a,b)=>a[1].label.localeCompare(b[1].label)).map(([key,group])=><optgroup key={key} label={group.label}>
     {group.projects.map(item=><option key={projectKey(item)} value={projectKey(item)}>{item.project} · {displayPath(item.projectPath)}</option>)}
    </optgroup>)}
   </select><ChevronDown size={13}/></label>
  </div>
  <AgentPicker counts={agentCounts} selected={selectedAgents} onChange={onAgentsChange}/>
  {currentProjects?.error && <p className="feedback error" role="alert">{currentProjects.error}</p>}
  <div className="report-actions">
   <button className="button primary" type="submit" disabled={busy || !currentProjects || !!currentProjects.error || (!!projectKeyValue && !selected)}>{busy ? <EagleMark flying/> : <FileText size={16}/>} {busy ? '正在提炼报告…' : saved?.report ? '重新生成' : '生成报告'}</button>
   <span className="report-action-period muted">{rangeLabel(range)}</span>
  </div>
  {connection && <p className="report-model-note muted">生成时将期间的工作回顾摘要发送给 {connection.name}。</p>}
  {legacyCache && <p className="report-model-note muted">检测到旧版浏览器缓存，其所属工作区无法确认。请在当前工作区重新生成报告。</p>}
 </form>
 {busy && <p role="status" className="visually-hidden">正在提炼报告</p>}
 {currentResult?.error && <p className="feedback error" role="alert">{currentResult.error}
  <button className="button text-button" type="button" onClick={()=>notifyReportListeners(scope)}>重试</button>
 </p>}
 {!busy && scope && !currentResult && <p className="muted" role="status">正在读取已保存的报告…</p>}
 {saved?.report && <ReportDocument key={scope} report={saved.report} draft={pending?.draft ?? saved.draft}
  dirty={dirty} saving={saving} canReplace={!busy && !!saved.draft}
  error={reportDraftErrors.get(scope)} onDraftChange={editDraft}
  onSave={()=>saveDraft(false)} onDiscard={discardPendingDraft} onReplace={()=>saveDraft(true)}
  period={rangeLabel(range)} project={selected?.project ?? ''} filename={`knooo-${from}-${to}.md`}
  onOpen={(id,message,version)=>onOpen(id,version ? message : undefined,'raw',version)}/>}
 {!busy && saved && !saved.report && <Empty icon={<FileText size={22}/>} title="选择期间后生成报告"><p>可在这里阅读、编辑和导出成稿。</p></Empty>}
 </>;
}

import {createRoot} from 'react-dom/client';
createRoot(document.getElementById('root')!).render(<App/>);
