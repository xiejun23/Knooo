import { ChevronLeft, ChevronRight } from 'lucide-react';

const defaultPageSizes = [20, 50, 100] as const;

type PageToken = number | 'ellipsis-left' | 'ellipsis-right';

function buildPageTokens(current: number, totalPages: number): PageToken[] {
 if (totalPages <= 7) {
  return Array.from({ length: totalPages }, (_, index) => index + 1);
 }
 const tokens: PageToken[] = [1];
 const start = Math.max(2, current - 1);
 const end = Math.min(totalPages - 1, current + 1);
 if (start > 2) tokens.push('ellipsis-left');
 for (let p = start; p <= end; p++) tokens.push(p);
 if (end < totalPages - 1) tokens.push('ellipsis-right');
 tokens.push(totalPages);
 return tokens;
}

export function Pagination({
 page,
 pageSize,
 total,
 pageSizes = defaultPageSizes,
 disabled = false,
 onPageChange,
 onPageSizeChange,
}: {
 page: number;
 pageSize: number;
 total: number;
 pageSizes?: readonly number[];
 disabled?: boolean;
 onPageChange: (nextPage: number) => void;
 onPageSizeChange?: (nextPageSize: number) => void;
}) {
 if (total <= 0) return null;
 const totalPages = Math.max(1, Math.ceil(total / pageSize));
 const safePage = Math.min(Math.max(1, page), totalPages);
 const startItem = (safePage - 1) * pageSize + 1;
 const endItem = Math.min(total, safePage * pageSize);
 const tokens = buildPageTokens(safePage, totalPages);

 return <nav className="pagination" aria-label="日志分页">
  <div className="pagination-summary">
   <span>第 {startItem}–{endItem} 条，共 {total} 条</span>
   {onPageSizeChange && <label className="pagination-size">
    <span>每页</span>
    <select aria-label="每页显示条数" value={pageSize} disabled={disabled}
     onChange={event => onPageSizeChange(Number(event.target.value))}>
     {pageSizes.map(size => <option key={size} value={size}>{size} 条</option>)}
    </select>
   </label>}
  </div>
  {totalPages > 1 && <div className="pagination-controls">
   <button type="button" className="pagination-nav" disabled={disabled || safePage <= 1}
    aria-label="上一页" onClick={() => onPageChange(safePage - 1)}>
    <ChevronLeft size={15} aria-hidden="true"/><span>上一页</span>
   </button>
   <div className="pagination-pages" role="group" aria-label="页码">
    {tokens.map(token => typeof token === 'number' ?
     <button key={token} type="button" className="pagination-page"
      disabled={disabled} aria-current={token === safePage ? 'page' : undefined}
      onClick={() => { if (token !== safePage) onPageChange(token); }}>{token}</button> :
     <span key={token} className="pagination-ellipsis" aria-hidden="true">…</span>)}
   </div>
   <span className="pagination-compact-indicator" aria-live="polite">{safePage} / {totalPages}</span>
   <button type="button" className="pagination-nav" disabled={disabled || safePage >= totalPages}
    aria-label="下一页" onClick={() => onPageChange(safePage + 1)}>
    <span>下一页</span><ChevronRight size={15} aria-hidden="true"/>
   </button>
  </div>}
 </nav>;
}
