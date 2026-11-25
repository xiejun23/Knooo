import { useState } from 'react';
import { ArrowDownToLine, ArrowUpRight, Check, Pencil } from 'lucide-react';

type ReportSource = {sessionId:string;messageId:string;project:string};
type ReportItem = {title:string;text:string;sources:ReportSource[]};
type ReportSection = {title:string;items:ReportItem[]};

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

export function ReportDocument({report,period,project,filename,onOpen}:{
 report:WorkReport;period:string;project:string;filename:string;onOpen:(id:string,message:string)=>void;
}) {
 const [draft,setDraft] = useState(report);
 const [editing,setEditing] = useState(false);
 const {active,ready,topics} = report.coverage;
 const coverage = `已提炼 ${ready} / ${active} 条会话 · ${topics} 项期间回顾`;
 const empty = !draft.sections.length;
 const emptyMessage = !active ? '所选期间没有会话活动。' : ready<active ? '期间会话仍在提炼，完成后可重新生成。' : '所选期间没有可纳入报告的工作要点。';

 function updateItem(sectionIndex:number,itemIndex:number,change:Partial<Pick<ReportItem,'title'|'text'>>) {
  setDraft(current=>({...current,sections:current.sections.map((section,i)=>i!==sectionIndex ? section : {
   ...section,items:section.items.map((item,j)=>j===itemIndex ? {...item,...change} : item),
  })}));
 }

 function download() {
  const md = draft===report ? report.markdown : [
   `# ${markdownText(draft.title) || report.title}`, project ? `${period} · ${markdownText(project)}` : period, markdownText(draft.summary ?? ''),
   ...draft.sections.map(section=>`## ${markdownText(section.title)}\n\n${section.items.map(item=>{
    const title=markdownText(item.title),text=markdownText(item.text);
    const projects=[...new Set(item.sources.map(source=>source.project).filter(Boolean))];
    const line=title && text ? `**${title}** · ${text}` : title || text;
    return line ? `- ${line}${projects.length ? `（${markdownText(projects.join('、'))}）` : ''}` : '';
   }).filter(Boolean).join('\n')}`),
   coverage, report.mode==='model' && '模型草稿，请按依据核对关键结论。', report.notice,
  ].filter(Boolean).join('\n\n')+'\n';
  const url = URL.createObjectURL(new Blob([md],{type:'text/markdown;charset=utf-8'}));
  const link = document.createElement('a');
  link.href=url;
  link.download=filename;
  link.click();
  setTimeout(()=>URL.revokeObjectURL(url),1000);
 }

 return <article className="report-document" aria-label="报告正文">
  <header className="report-document-head">
   <div className="report-document-meta">
    <span>{period}{project && ` · ${project}`}</span>
    <div className="report-document-actions">
     {!empty && <button type="button" className="button text-button" onClick={()=>setEditing(!editing)}>{editing ? <Check size={15}/> : <Pencil size={15}/>} {editing ? '完成编辑' : '编辑'}</button>}
     <button type="button" className="button secondary" onClick={download}><ArrowDownToLine size={15}/> 导出 Markdown</button>
    </div>
   </div>
   {editing ? <input aria-label="报告标题" value={draft.title} onChange={event=>setDraft({...draft,title:event.target.value})}/> : <h2>{draft.title || report.title}</h2>}
   {editing ? <textarea aria-label="报告摘要" rows={2} value={draft.summary ?? ''} onChange={event=>setDraft({...draft,summary:event.target.value})}/> : draft.summary && <p className="report-lead">{draft.summary}</p>}
  </header>
  {empty ? <p className="report-document-empty">{emptyMessage}</p> : <div className="report-sections">
   {draft.sections.map((section,i)=><section key={section.title} className="report-section">
    <h3>{section.title}</h3>
    <ul>{section.items.map((item,j)=><li key={j}>
     {editing ? <div className="report-edit-fields">
      <input aria-label={`${section.title}第 ${j+1} 项标题`} value={item.title} onChange={event=>updateItem(i,j,{title:event.target.value})}/>
      <textarea aria-label={`${section.title}第 ${j+1} 项正文`} rows={2} value={item.text} onChange={event=>updateItem(i,j,{text:event.target.value})}/>
     </div> : <><h4>{item.title}</h4>{item.text && <p>{item.text}</p>}</>}
     {item.sources.length>0 && <details className="report-sources">
      <summary>{item.sources.length} 条{draft===report ? '依据' : '原稿依据'}</summary>
      <div>{item.sources.map((source,k)=><button className="button text-button" type="button" key={`${source.sessionId}-${source.messageId}`} onClick={()=>onOpen(source.sessionId,source.messageId)}>
       {source.project || '会话'} · 原文 {k+1}<ArrowUpRight size={13}/>
      </button>)}</div>
     </details>}
    </li>)}</ul>
   </section>)}
  </div>}
  <footer className="report-document-foot">
   {active>0 && <p>{report.mode==='model' ? '模型草稿' : '本地整理'} · {coverage}{ready<active && '。后台提炼完成后可重新生成。'}</p>}
   {report.mode==='model' && <p>可展开依据，核对关键结论。</p>}
   {report.notice && <p>{report.notice}</p>}
  </footer>
 </article>;
}
