import { useEffect, useId, useRef, useState, type ReactNode } from 'react';
import { CalendarDays, ChevronRight } from 'lucide-react';

export type DateInterval = { from: string; to: string };

const twoDigits = (value: number) => String(value).padStart(2, '0');

export function dayStart(days = 0) {
 const d = new Date();
 d.setHours(0, 0, 0, 0);
 d.setDate(d.getDate() + days);
 return d;
}

export const dateValue = (d: Date) =>
 `${String(d.getFullYear()).padStart(4, '0')}-${twoDigits(d.getMonth() + 1)}-${twoDigits(d.getDate())}`;

export const rangeLabel = ({ from, to }: DateInterval) => (from === to ? from : `${from} 至 ${to}`);

export function ReviewDropdown({ label, trigger, children, className = '' }: {
 label: string; trigger: ReactNode; children: (close: () => void) => ReactNode; className?: string;
}) {
 const details = useRef<HTMLDetailsElement>(null);
 const summary = useRef<HTMLElement>(null);
 const panel = useRef<HTMLDivElement>(null);
 function close(restoreFocus = true) {
  if (!details.current?.open) return;
  details.current.open = false;
  if (restoreFocus) summary.current?.focus();
 }
 useEffect(() => {
  function dismiss(event: PointerEvent) {
   if (event.target instanceof Node && !details.current?.contains(event.target)) close(false);
  }
  document.addEventListener('pointerdown', dismiss);
  return () => document.removeEventListener('pointerdown', dismiss);
 }, []);
 return <details ref={details} className={`review-dropdown ${className}`} onToggle={() => {
  if (details.current?.open) panel.current?.scrollIntoView({ block: 'nearest', inline: 'nearest' });
 }} onKeyDown={event => {
  if (event.key !== 'Escape' || !details.current?.open) return;
  event.preventDefault();
  close();
 }}>
  <summary ref={summary} className="button secondary" aria-label={label} title={label}>{trigger}</summary>
  <div ref={panel} className="review-dropdown-panel">{children(close)}</div>
 </details>;
}

export function DateRangePicker({ range, active, showRange, onChange }: {
 range: DateInterval; active: boolean; showRange?: boolean; onChange: (range: DateInterval) => void;
}) {
 return <ReviewDropdown label="自定义日期范围" className={`review-calendar${active ? ' has-range' : ''}`}
  trigger={<><CalendarDays size={16}/>{active && showRange && <span>{rangeLabel(range)}</span>}</>}>
  {close => <ReviewCalendar key={`${range.from}:${range.to}`} initial={range} onApply={next => { close(); onChange(next); }}/>}
 </ReviewDropdown>;
}

export function ReviewCalendar({ initial, onApply }: { initial: DateInterval; onApply: (range: DateInterval) => void }) {
 const [draft, setDraft] = useState(initial);
 const [anchor, setAnchor] = useState<string>();
 const [month, setMonth] = useState(() => new Date(`${initial.to.slice(0, 7)}-01T00:00:00`));
 const [focusedDay, setFocusedDay] = useState(() => Number(initial.to.slice(8)));
 const year = month.getFullYear();
 const monthIndex = month.getMonth();
 const firstColumn = (month.getDay() + 6) % 7 + 1;
 const days = new Date(year, monthIndex + 1, 0).getDate();
 const today = dateValue(dayStart());
 const monthHeading = useId();
 function changeMonth(offset: number) {
  setMonth(new Date(year, monthIndex + offset, 1));
  setFocusedDay(1);
 }
 function choose(date: string) {
  if (anchor) {
   setDraft(date < anchor ? { from: date, to: anchor } : { from: anchor, to: date });
   setAnchor(undefined);
  } else {
   setDraft({ from: date, to: date });
   setAnchor(date);
  }
 }
 return <div className="review-date-picker">
  <div className="review-month-bar">
   <button className="icon-button" type="button" aria-label="上个月" onClick={() => changeMonth(-1)}><ChevronRight className="previous-month" size={16}/></button>
   <strong id={monthHeading} aria-live="polite">{year} 年 {monthIndex + 1} 月</strong>
   <button className="icon-button" type="button" aria-label="下个月" onClick={() => changeMonth(1)}><ChevronRight size={16}/></button>
  </div>
  <div className="review-month-days" role="group" aria-labelledby={monthHeading}>
   {['一', '二', '三', '四', '五', '六', '日'].map(day => <span key={day} aria-hidden="true">{day}</span>)}
   {Array.from({ length: days }, (_, index) => {
    const day = index + 1;
    const date = dateValue(new Date(year, monthIndex, day));
    const selected = date >= draft.from && date <= draft.to;
    return <button key={date} className="review-day" type="button" aria-label={date} aria-pressed={selected}
     aria-current={date === today ? 'date' : undefined} data-edge={date === draft.from || date === draft.to}
     data-day={day} tabIndex={day === focusedDay ? 0 : -1} style={day === 1 ? { gridColumnStart: firstColumn } : undefined}
     onFocus={() => setFocusedDay(day)} onClick={() => choose(date)} onKeyDown={event => {
      const offset = { ArrowLeft: -1, ArrowRight: 1, ArrowUp: -7, ArrowDown: 7 }[event.key];
      if (offset === undefined) return;
      event.preventDefault();
      event.currentTarget.parentElement?.querySelector<HTMLButtonElement>(`[data-day="${day + offset}"]`)?.focus();
     }}>{day}</button>;
   })}
  </div>
  <div className="review-date-actions">
   <span role="status">{anchor ? `${anchor} · 选择结束日期` : `${draft.from}${draft.from === draft.to ? '' : ` 至 ${draft.to}`}`}</span>
   <button className="button primary" type="button" onClick={() => onApply(draft)}>应用日期</button>
  </div>
 </div>;
}
