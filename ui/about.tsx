import { useMemo, useState } from 'react';
import { ArrowDownToLine, ArrowUpRight, Mail } from 'lucide-react';
import { EagleMark } from './eagle';
import {
 filterReleases,
 formatChangelogDate,
 kindLabel,
 parseChangelog,
 summarizeKinds,
 summarizeMonths,
 type ChangelogFilter,
} from './changelog';

const feedbackEmails = ['xebron88823@gmail.com', 'xebron@163.com'];

export type About = { version: string; changelog: string; repository: string; demo: boolean };
export type Update = { enabled: boolean; canInstall: boolean; available: { version: string } | null };
export type UpdateActivity = 'idle' | 'checking' | 'installing' | 'started';

export function UpdateButton({ update, activity, repository, onInstall }: {
 update: Update; activity: UpdateActivity; repository: string; onInstall: () => void;
}) {
 if (!update.available) return null;
 const { version } = update.available;
 if (!update.canInstall) {
  return <a className="button primary update-button" href={`${repository}/releases/tag/${version}`}
   target="_blank" rel="noreferrer"><ArrowUpRight size={15}/>下载 {version}</a>;
 }
 const label = activity === 'installing' ? '正在下载更新…' : activity === 'started' ? '正在安装…' : `更新到 ${version}`;
 return <button className="button primary update-button" disabled={activity !== 'idle'} onClick={onInstall}>
  <ArrowDownToLine size={15}/>{label}
 </button>;
}

export function AboutContent({ about, update, activity, updateError, onCheck, onInstall }: {
 about: About; update: Update | undefined; activity: UpdateActivity; updateError: string;
 onCheck: () => void; onInstall: () => void;
}) {
 const [selectedKind, setSelectedKind] = useState<ChangelogFilter>('all');
 const [selectedMonth, setSelectedMonth] = useState('');

 const releases = useMemo(() => parseChangelog(about.changelog), [about.changelog]);
 const kindItems = useMemo(() => summarizeKinds(releases), [releases]);
 const months = useMemo(() => summarizeMonths(releases, selectedKind), [releases, selectedKind]);
 const activeMonth = months.some(month => month.key === selectedMonth) ? selectedMonth : '';
 const visibleReleases = useMemo(
  () => filterReleases(releases, selectedKind, activeMonth),
  [releases, selectedKind, activeMonth],
 );
 const visibleCount = useMemo(
  () => visibleReleases.reduce((sum, release) => sum + release.entries.length, 0),
  [visibleReleases],
 );

 const activeKindLabel = kindItems.find(item => item.id === selectedKind)?.label ?? '全部';
 const activeMonthLabel = months.find(month => month.key === activeMonth)?.label ?? '';
 const hasFilter = selectedKind !== 'all' || activeMonth !== '';

 const selectKind = (next: ChangelogFilter) => {
  setSelectedKind(next);
  if (activeMonth) {
   const nextMonths = summarizeMonths(releases, next);
   if (!nextMonths.some(month => month.key === activeMonth)) setSelectedMonth('');
  }
 };

 const toggleMonth = (key: string) => {
  setSelectedMonth(current => (current === key ? '' : key));
 };

 const clearFilters = () => {
  setSelectedKind('all');
  setSelectedMonth('');
 };

 const feedbackDraft = `subject=${encodeURIComponent('Knooo 使用反馈')}&body=${encodeURIComponent(`\r\n\r\nKnooo 版本：v${about.version}`)}`;
 let status = '';
 if (update?.enabled) {
  status = update.available ? `${update.available.version} 可用` : '没有可用更新。';
  if (update.available && !update.canInstall) status += ' 请前往 GitHub 下载。';
 }
 if (activity === 'checking') status = '正在检查更新…';
 if (activity === 'installing') status = '正在下载安装包并校验…';
 if (activity === 'started') status = '安装器已启动，安装完成后会打开新版。';

 return <div className="about-page">
  <section className="about-card" aria-label="版本与更新">
   <EagleMark/>
   <div className="about-version"><h2>Knooo</h2><p>当前版本 v{about.version}</p></div>
   <div className="about-actions">
    {!about.demo && <button className="button secondary" onClick={onCheck} disabled={activity !== 'idle'}>检查更新</button>}
    {update?.available && <UpdateButton update={update} activity={activity} repository={about.repository} onInstall={onInstall}/>}
    <a className="button secondary" href={`${about.repository}/releases`} target="_blank" rel="noreferrer">
     GitHub Releases <ArrowUpRight size={15}/>
    </a>
   </div>
   {status && !updateError && <p className="about-status" role="status">{status}</p>}
   {updateError && <p className="about-status about-error" role="alert">{updateError}</p>}
  </section>

  <div className="about-layout">
   <section className="changelog-main" aria-labelledby="changelog-heading">
    <header className="changelog-header">
     <div>
      <h2 id="changelog-heading">更新日志</h2>
      <p>新功能、优化、公告与下线变更，都记录在这里。</p>
     </div>
     {hasFilter && <div className="changelog-active-filter">
      <span>
       当前筛选：{selectedKind !== 'all' ? activeKindLabel : '全部'}
       {activeMonthLabel ? ` · ${activeMonthLabel}` : ''}
       （{visibleCount} 条）
      </span>
      <button type="button" className="text-button" onClick={clearFilters}>清除筛选</button>
     </div>}
    </header>

    <div className="changelog-compact-filters" aria-label="日志筛选">
     <div className="changelog-pill-strip" role="group" aria-label="按类型看">
      {kindItems.map(item => <button key={item.id} type="button" className="changelog-pill"
       aria-pressed={selectedKind === item.id} onClick={() => selectKind(item.id)}>
       <span className="changelog-dot" data-kind={item.id} aria-hidden="true"/>
       <span>{item.label}</span>
       <span className="changelog-pill-count">{item.count}</span>
      </button>)}
     </div>
     {months.length > 0 && <div className="changelog-pill-strip" role="group" aria-label="按月份">
      <button type="button" className="changelog-pill" aria-pressed={activeMonth === ''}
       onClick={() => setSelectedMonth('')}>
       <span>全部月份</span>
      </button>
      {months.map(month => <button key={month.key} type="button" className="changelog-pill"
       aria-pressed={activeMonth === month.key} onClick={() => toggleMonth(month.key)}>
       <span>{month.label}</span>
       <span className="changelog-pill-count">{month.count} 条</span>
      </button>)}
     </div>}
    </div>

    {visibleReleases.length > 0 ? <div className="changelog-feed">
     {visibleReleases.map(release => {
      const releaseDate = formatChangelogDate(release.date);
      return <section key={release.id} className="changelog-card" aria-label={release.rawTitle}>
       <div className="changelog-card-head">
        <div className="changelog-card-title">
         <h3>{release.version}</h3>
         {release.subtitle && <span className="changelog-release-tag">{release.subtitle}</span>}
         {releaseDate.full && <time dateTime={release.date} className="changelog-card-date">
          {releaseDate.full}
          {releaseDate.weekday && <span>{releaseDate.weekday}</span>}
         </time>}
        </div>
        <span className="changelog-card-count">{release.entries.length} 条</span>
       </div>
       <ol className="changelog-entries">
        {release.entries.map(entry => {
         const entryDate = formatChangelogDate(entry.date);
         const metaLabel = entryDate.short || entry.version;
         return <li key={entry.id} className="changelog-entry">
          <div className="changelog-entry-meta">
           <span className="changelog-entry-stamp">{metaLabel}</span>
           <span className="changelog-kind">
            <span className="changelog-dot" data-kind={entry.kind} aria-hidden="true"/>
            <span>{kindLabel(entry.kind)}</span>
           </span>
          </div>
          <article className="changelog-entry-body">
           <h4>{entry.title}</h4>
           {entry.detail && <p>{entry.detail}</p>}
          </article>
         </li>;
        })}
       </ol>
      </section>;
     })}
    </div> : <div className="empty changelog-empty">
     <h3>当前筛选下暂无日志</h3>
     <p>试试切换到其他类型或查看全部更新日志。</p>
     <button type="button" className="button secondary" onClick={clearFilters}>查看全部</button>
    </div>}
   </section>

   <aside className="about-sidebar" aria-label="更新日志筛选与反馈">
    <section className="changelog-side-card changelog-side-filter" aria-labelledby="changelog-kind-heading">
     <h3 id="changelog-kind-heading">按类型看</h3>
     <div className="changelog-side-list">
      {kindItems.map(item => <button key={item.id} type="button" className="changelog-filter-row"
       data-empty={item.count === 0 ? 'true' : undefined}
       aria-pressed={selectedKind === item.id} onClick={() => selectKind(item.id)}>
       <span className="changelog-dot" data-kind={item.id} aria-hidden="true"/>
       <span className="changelog-filter-label">{item.label}</span>
       <span className="changelog-filter-count">{item.count}</span>
      </button>)}
     </div>
    </section>

    {months.length > 0 && <section className="changelog-side-card changelog-side-filter" aria-labelledby="changelog-month-heading">
     <div className="changelog-side-head">
      <h3 id="changelog-month-heading">按月份</h3>
      {activeMonth && <button type="button" className="text-button" onClick={() => setSelectedMonth('')}>全部</button>}
     </div>
     <div className="changelog-side-list">
      {months.map(month => <button key={month.key} type="button" className="changelog-month-row"
       aria-pressed={activeMonth === month.key} onClick={() => toggleMonth(month.key)}>
       <span className="changelog-filter-label">{month.label}</span>
       <span className="changelog-filter-count">{month.count} 条</span>
      </button>)}
     </div>
    </section>}

    <section className="changelog-side-card about-feedback" aria-labelledby="feedback-heading">
     <h3 id="feedback-heading">说说你的想法</h3>
     <p>遇到 Bug、想要一个新功能，或者有哪处用起来不顺手、看着不顺眼，都欢迎写信告诉我。</p>
     <div className="feedback-emails">
      {feedbackEmails.map(email => <a key={email} className="button secondary" href={`mailto:${email}?${feedbackDraft}`}>
       <Mail size={15} aria-hidden="true"/><span>{email}</span>
      </a>)}
     </div>
     <p className="feedback-note">两个邮箱都可以联系到我，我会尽快回复。谢谢你愿意花时间分享！</p>
    </section>
   </aside>
  </div>
 </div>;
}
