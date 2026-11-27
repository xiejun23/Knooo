import { useState } from 'react';
import { ArrowDownToLine, ArrowUpRight, Check, Pencil } from 'lucide-react';

type ReportSource = {sessionId:string;messageId:string;project:string;evidenceVersion?:string};
type ReportItem = {title:string;text:string;status?:string;sources:ReportSource[]};
type ReportSection = {title:string;items:ReportItem[];supplemental?:boolean};

export type WorkReport = {
 title:string;
 summary?:string;
 sections:ReportSection[];
 coverage:{ready:number;active:number;topics:number};
 mode:'local'|'model';
 notice?:string;
 markdown:string;
};

function markdownText(text:string) {
 return text.trim().replace(/[\\`*_[\]<>|&#~!+=.()-]/g,'\\$&').replace(/\s+/g,' ');
}

function needsFollowup(item:ReportItem) {
 return item.status==='待确认' || (item.status?.includes('待跟进') ?? false);
}

export function ReportDocument({report,draft:edited,dirty,saving,canReplace,error,onDraftChange,onSave,onDiscard,onReplace,period,project,filename,onOpen}:{
 report:WorkReport;draft:WorkReport|null;dirty:boolean;saving:boolean;canReplace:boolean;error?:string;
 onDraftChange:(draft:WorkReport)=>void;onSave:()=>void;onDiscard:()=>void;onReplace:()=>void;
 period:string;project:string;filename:string;onOpen:(id:string,message:string,evidenceVersion?:string)=>void;
}) {
 const [editing,setEditing] = useState(false);
 const [followupOnly,setFollowupOnly] = useState(false);
 const draft = edited ?? report;
 const {active,ready,topics} = draft.coverage;
 const coverage = `已提炼 ${ready} / ${active} 条会话 · ${topics} 项期间回顾`;
 const empty = !draft.sections.length;
 const emptyMessage = !active ? '所选期间没有会话活动。' : ready<active ? '期间会话仍在提炼，完成后可重新生成。' : '所选期间没有可纳入报告的工作要点。';
 const followupCount = draft.sections.reduce((count,section)=>count+section.items.filter(needsFollowup).length,0);
 const filtering = followupOnly && followupCount>0;

 function updateItem(sectionIndex:number,itemIndex:number,change:Partial<Pick<ReportItem,'title'|'text'>>) {
  onDraftChange({...draft,sections:draft.sections.map((section,i)=>i!==sectionIndex ? section : {
   ...section,items:section.items.map((item,j)=>j===itemIndex ? {...item,...change} : item),
  })});
 }

 function download() {
  const md = draft===report ? report.markdown : [
   `# ${markdownText(draft.title) || report.title}`, project ? `${period} · ${markdownText(project)}` : period, markdownText(draft.summary ?? ''),
   ...draft.sections.map(section=>`## ${section.supplemental ? '补充事项 · ' : ''}${markdownText(section.title)}\n\n${section.items.map(item=>{
    const title=markdownText(item.title),text=markdownText(item.text);
    const projects=[...new Set(item.sources.map(source=>source.project).filter(Boolean))];
    const line=[title && `**${title}**`,markdownText(item.status ?? ''),text].filter(Boolean).join(' · ');
    return line ? `- ${line}${projects.length ? `（${markdownText(projects.join('、'))}）` : ''}` : '';
   }).filter(Boolean).join('\n')}`),
   coverage, draft.mode==='model' && '模型草稿，请按依据核对关键结论。', draft.notice,
  ].filter(Boolean).join('\n\n')+'\n';
  const url = URL.createObjectURL(new Blob([md],{type:'text/markdown;charset=utf-8'}));
  const link = document.createElement('a');
  link.href=url;
  link.download=filename;
  link.click();
  setTimeout(()=>URL.revokeObjectURL(url),1000);
 }

 const mainSections = [];
 const supplementalSections = [];
 let supplementalCount = 0;
 for (const [i,section] of draft.sections.entries()) {
  const sectionLabel = section.supplemental ? `补充事项 · ${section.title}` : section.title;
  const items = section.items.map((item,j)=>{
   if (filtering && !needsFollowup(item)) return null;
   return <li key={j}>
    {editing ? <div className="report-edit-fields">
     <input aria-label={`${sectionLabel}第 ${j+1} 项标题`} value={item.title} readOnly={saving} onChange={event=>updateItem(i,j,{title:event.target.value})}/>
     <textarea aria-label={`${sectionLabel}第 ${j+1} 项正文`} rows={2} value={item.text} readOnly={saving} onChange={event=>updateItem(i,j,{text:event.target.value})}/>
    </div> : <><h4>{item.title}</h4>{item.text && <p>{item.text}</p>}</>}
    {item.status && <small className="muted report-item-status">{item.status}</small>}
    {item.sources.length>0 && <details className="report-sources">
     <summary>{item.sources.length} 条{draft===report ? '依据' : '原稿依据'}</summary>
     <div>{item.sources.map((source,k)=><button className="button text-button" type="button" key={`${source.sessionId}-${source.messageId}`} onClick={()=>onOpen(source.sessionId,source.messageId,source.evidenceVersion)}>
      {source.project || '会话'} · 原文 {k+1}<ArrowUpRight size={13}/>
     </button>)}</div>
    </details>}
   </li>;
  }).filter(item=>item!==null);
  if (!items.length) continue;
  const content = <section key={section.title} className="report-section">
   <h3>{section.title}</h3>
   <ul>{items}</ul>
  </section>;
  if (section.supplemental) {
   supplementalSections.push(content);
   supplementalCount += items.length;
  } else {
   mainSections.push(content);
  }
 }

 return <article className="report-document" aria-label="报告正文">
  <header className="report-document-head">
   <div className="report-document-meta">
    <span>{period}{project && ` · ${project}`}</span>
    <div className="report-document-actions">
     {!empty && <button type="button" className="button text-button" disabled={saving} onClick={()=>setEditing(!editing)}>{editing ? <Check size={15}/> : <Pencil size={15}/>} {editing ? '完成编辑' : '编辑'}</button>}
     {dirty && <button type="button" className="button primary" disabled={saving} onClick={onSave}>{saving ? '正在保存…' : '保存编辑'}</button>}
     {dirty && <button type="button" className="button text-button" disabled={saving} onClick={()=>{
      if (window.confirm('放弃此次尚未保存的编辑？')) onDiscard();
     }}>放弃本次编辑</button>}
     {canReplace && <button type="button" className="button text-button" disabled={saving} onClick={()=>{
      if (window.confirm('使用生成稿并放弃此报告的编辑内容？')) onReplace();
     }}>使用生成稿</button>}
     <button type="button" className="button secondary" onClick={download}><ArrowDownToLine size={15}/> 导出 Markdown</button>
    </div>
   </div>
   {error && <p className="feedback error" role="alert">{error}</p>}
   {editing ? <input aria-label="报告标题" value={draft.title} readOnly={saving} onChange={event=>onDraftChange({...draft,title:event.target.value})}/> : <h2>{draft.title || report.title}</h2>}
   {editing ? <textarea aria-label="报告摘要" rows={2} value={draft.summary ?? ''} readOnly={saving} onChange={event=>onDraftChange({...draft,summary:event.target.value})}/> : draft.summary && <p className="report-lead">{draft.summary}</p>}
  </header>
  {empty ? <p className="report-document-empty">{emptyMessage}</p> : <div className="report-sections">
   {followupCount>0 && <div className="segments report-item-filter" role="group" aria-label="报告事项筛选">
    <button type="button" aria-pressed={!filtering} onClick={()=>setFollowupOnly(false)}>全部事项</button>
    <button type="button" aria-pressed={filtering} onClick={()=>setFollowupOnly(true)}>待跟进 {followupCount} 项</button>
   </div>}
   {mainSections}
   {supplementalCount>0 && <details className="report-supplement" open={editing || filtering || undefined}>
    <summary>补充事项 {supplementalCount} 项</summary>
    {supplementalSections}
   </details>}
  </div>}
  <footer className="report-document-foot">
   {active>0 && <p>{draft.mode==='model' ? '模型草稿' : '本地整理'} · {coverage}{ready<active && '。后台提炼完成后可重新生成。'}</p>}
   {draft.mode==='model' && <p>可展开依据，核对关键结论。</p>}
   {draft.notice && <p>{draft.notice}</p>}
  </footer>
 </article>;
}
