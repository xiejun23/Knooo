export type ChangelogKind = 'update' | 'improve' | 'notice' | 'sunset' | 'fix';
export type ChangelogFilter = 'all' | ChangelogKind;

export type ChangelogKindMeta = {
 id: ChangelogKind;
 label: string;
 core: boolean;
};

export const CHANGELOG_KINDS: ChangelogKindMeta[] = [
 { id: 'update', label: '更新', core: true },
 { id: 'improve', label: '优化', core: true },
 { id: 'notice', label: '公告', core: true },
 { id: 'sunset', label: '下线', core: true },
 { id: 'fix', label: '修复', core: false },
];

const kindAliases: Record<string, ChangelogKind> = {
 更新: 'update',
 新增: 'update',
 新功能: 'update',
 优化: 'improve',
 改进: 'improve',
 调整: 'improve',
 公告: 'notice',
 说明: 'notice',
 通知: 'notice',
 下线: 'sunset',
 移除: 'sunset',
 废弃: 'sunset',
 修复: 'fix',
 修正: 'fix',
};

const kindLabels: Record<ChangelogKind, string> = {
 update: '更新',
 improve: '优化',
 notice: '公告',
 sunset: '下线',
 fix: '修复',
};

const weekdays = ['周日', '周一', '周二', '周三', '周四', '周五', '周六'];
const dateToken = /^(\d{4})-(\d{2})(?:-(\d{2}))?$/;

export type ChangelogEntry = {
 id: string;
 version: string;
 kind: ChangelogKind;
 date: string;
 monthKey: string;
 title: string;
 detail: string;
};

export type ChangelogRelease = {
 id: string;
 rawTitle: string;
 version: string;
 subtitle: string;
 date: string;
 entries: ChangelogEntry[];
};

export type MonthSummary = {
 key: string;
 label: string;
 count: number;
};

export type FormattedDate = {
 full: string;
 weekday: string;
 short: string;
 monthKey: string;
 monthLabel: string;
};

export function kindLabel(kind: ChangelogKind): string {
 return kindLabels[kind];
}

export function formatChangelogDate(raw: string): FormattedDate {
 const match = dateToken.exec(raw.trim());
 if (!match) return { full: '', weekday: '', short: '', monthKey: '', monthLabel: '' };
 const year = Number(match[1]);
 const month = Number(match[2]);
 const day = match[3] ? Number(match[3]) : null;
 const monthKey = `${match[1]}-${match[2]}`;
 const monthLabel = `${year} 年 ${month} 月`;
 if (day === null) {
  return { full: monthLabel, weekday: '', short: monthKey, monthKey, monthLabel };
 }
 const localDate = new Date(year, month - 1, day);
 const weekday = Number.isNaN(localDate.getTime()) ? '' : weekdays[localDate.getDay()];
 return {
  full: `${year} 年 ${month} 月 ${day} 日`,
  weekday,
  short: `${match[2]}-${match[3]}`,
  monthKey,
  monthLabel,
 };
}

function parseReleaseHeading(rawTitle: string): { version: string; date: string; subtitle: string } {
 const parts = rawTitle.split(/\s*·\s*/).map(part => part.trim()).filter(Boolean);
 const version = parts[0] ?? rawTitle.trim();
 let date = '';
 const rest: string[] = [];
 for (const part of parts.slice(1)) {
  if (!date && dateToken.test(part)) date = part;
  else rest.push(part);
 }
 return { version, date, subtitle: rest.join(' · ') };
}

export function parseChangelog(markdown: string): ChangelogRelease[] {
 const releases: ChangelogRelease[] = [];
 let activeDate = '';
 let itemIndex = 0;

 for (const rawLine of markdown.split(/\r?\n/)) {
  const line = rawLine.trim();
  if (line.startsWith('## ')) {
   const rawTitle = line.slice(3).trim();
   const parsed = parseReleaseHeading(rawTitle);
   activeDate = parsed.date;
   releases.push({
    id: `release-${releases.length}-${parsed.version}`,
    rawTitle,
    version: parsed.version,
    subtitle: parsed.subtitle,
    date: parsed.date,
    entries: [],
   });
   continue;
  }
  if (line.startsWith('### ')) {
   const candidate = line.slice(4).trim();
   if (dateToken.test(candidate)) activeDate = candidate;
   continue;
  }
  if (!line.startsWith('- ')) continue;
  const current = releases.at(-1);
  if (!current) continue;

  let text = line.slice(2).trim();
  if (!text) continue;

  let kind: ChangelogKind = 'update';
  const kindMatch = /^\[([^\]]+)\]\s*/.exec(text);
  if (kindMatch) {
   const mapped = kindAliases[kindMatch[1].trim()];
   if (mapped) {
    kind = mapped;
    text = text.slice(kindMatch[0].length);
   }
  }

  let entryDate = activeDate;
  const inlineDateMatch = /^(\d{4}-\d{2}(?:-\d{2})?)\s+/.exec(text);
  if (inlineDateMatch) {
   entryDate = inlineDateMatch[1];
   text = text.slice(inlineDateMatch[0].length);
  }

  let title = text;
  let detail = '';
  const boldMatch = /^\*\*([^*]+)\*\*(?:\s*[：:]\s*(.*))?$/.exec(text);
  if (boldMatch) {
   title = boldMatch[1].trim();
   detail = (boldMatch[2] ?? '').trim();
  }

  const formatted = formatChangelogDate(entryDate);
  current.entries.push({
   id: `entry-${itemIndex++}`,
   version: current.version,
   kind,
   date: entryDate,
   monthKey: formatted.monthKey,
   title,
   detail,
  });
 }

 return releases;
}

export function summarizeKinds(releases: ChangelogRelease[]): { id: ChangelogFilter; label: string; count: number }[] {
 const counts = new Map<ChangelogKind, number>();
 let total = 0;
 for (const release of releases) {
  for (const entry of release.entries) {
   total++;
   counts.set(entry.kind, (counts.get(entry.kind) ?? 0) + 1);
  }
 }
 const items: { id: ChangelogFilter; label: string; count: number }[] = [
  { id: 'all', label: '全部', count: total },
 ];
 for (const meta of CHANGELOG_KINDS) {
  const count = counts.get(meta.id) ?? 0;
  if (meta.core || count > 0) {
   items.push({ id: meta.id, label: meta.label, count });
  }
 }
 return items;
}

export function summarizeMonths(releases: ChangelogRelease[], kind: ChangelogFilter): MonthSummary[] {
 const counts = new Map<string, MonthSummary>();
 for (const release of releases) {
  for (const entry of release.entries) {
   if (kind !== 'all' && entry.kind !== kind) continue;
   if (!entry.monthKey) continue;
   const existing = counts.get(entry.monthKey);
   if (existing) {
    existing.count++;
   } else {
    const formatted = formatChangelogDate(entry.date);
    counts.set(entry.monthKey, {
     key: entry.monthKey,
     label: formatted.monthLabel || entry.monthKey,
     count: 1,
    });
   }
  }
 }
 return [...counts.values()].sort((a, b) => b.key.localeCompare(a.key));
}

export function filterReleases(
 releases: ChangelogRelease[],
 kind: ChangelogFilter,
 monthKey: string,
): ChangelogRelease[] {
 const result: ChangelogRelease[] = [];
 for (const release of releases) {
  const entries = release.entries.filter(entry =>
   (kind === 'all' || entry.kind === kind) && (!monthKey || entry.monthKey === monthKey),
  );
  if (entries.length > 0) {
   result.push({ ...release, entries });
  }
 }
 return result;
}
