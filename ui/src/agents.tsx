/// <reference types="vite/client" />
import { Workflow } from 'lucide-react';
import codexIcon from './assets/agents/codex.png';
import claudeIcon from './assets/agents/claude.svg?no-inline';
import geminiIcon from './assets/agents/gemini.png';
import copilotIcon from './assets/agents/copilot.svg?no-inline';
import qwenIcon from './assets/agents/qwen.svg?no-inline';
import piIcon from './assets/agents/pi.svg?no-inline';
import workbuddyIcon from './assets/agents/workbuddy.svg?no-inline';
import deepseekIcon from './assets/agents/deepseek.svg?no-inline';
import antigravityIcon from './assets/agents/antigravity.svg?no-inline';

const agents = {
 codex: { name: 'Codex', icon: codexIcon },
 claude: { name: 'Claude Code', icon: claudeIcon },
 pi: { name: 'Pi', icon: piIcon },
 workbuddy: { name: 'WorkBuddy', icon: workbuddyIcon },
 dsh: { name: 'DeepSeek Harness', icon: deepseekIcon },
 antigravity: { name: 'Antigravity', icon: antigravityIcon },
 gemini: { name: 'Gemini CLI', icon: geminiIcon },
 copilot: { name: 'GitHub Copilot CLI', icon: copilotIcon },
 qwen: { name: 'Qwen Code', icon: qwenIcon },
} as const;

export type Agent = keyof typeof agents;
export type AgentSource = {agent: Agent; agentVariant?: 'cli' | 'app'};
type AgentKey = Agent | 'antigravity:cli' | 'antigravity:app';
const variants = {
 'antigravity:cli': { name: 'Antigravity CLI', icon: antigravityIcon },
 'antigravity:app': { name: 'Antigravity App', icon: antigravityIcon },
} as const;
const badges = {...agents,...variants};
export const agentKey = ({agent,agentVariant}:AgentSource):AgentKey =>
 agent==='antigravity' && agentVariant ? `antigravity:${agentVariant}` : agent;
export const agentName = (source:AgentSource) => badges[agentKey(source)]?.name ?? source.agent;

export function AgentBadge({source}: {source:AgentSource}) {
 const icon = badges[agentKey(source)]?.icon;
 return <span className="agent">{icon && <img src={icon} width="16" height="16" alt=""/>}{agentName(source)}</span>;
}

export function countAgents(sessions: readonly AgentSource[]) {
 const counts = new Map<string,number>();
 for (const session of sessions) {
  const key=agentKey(session);
  counts.set(key,(counts.get(key) ?? 0)+1);
 }
 return counts;
}

export function agentQuery(params: Record<string, string>, selected: readonly string[]) {
 const query = new URLSearchParams(params);
 for (const agent of selected) query.append('agent', agent);
 return query.toString();
}

export function AgentPicker({counts, selected, onChange, disabled}: {
 counts:ReadonlyMap<string,number>; selected:readonly string[]; onChange:(agents:string[])=>void; disabled?:boolean;
}) {
 if (!counts.size) return null;
 return <div className="agent-picker" role="group" aria-label="筛选 Agent">
  <button type="button" aria-pressed={!selected.length} disabled={disabled} onClick={()=>onChange([])}><Workflow size={16}/>全部 Agent</button>
  {Object.entries(badges).filter(([key])=>counts.has(key)).map(([key,{name,icon}])=>{
   const pressed = selected.includes(key);
   const count = counts.get(key);
   return <button type="button" key={key} aria-label={`${name}，${count} 条会话`} aria-pressed={pressed} disabled={disabled}
    onClick={()=>onChange(pressed ? selected.filter(agent=>agent!==key) : [...selected,key].sort())}>
    <span className="agent"><img src={icon} width="16" height="16" alt=""/>{name}</span><span className="agent-count">{count}</span>
   </button>;
  })}
 </div>;
}
