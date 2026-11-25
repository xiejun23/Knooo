import type { KeyboardEvent, PointerEvent } from 'react';

export function PanelGrip({ side, width, min, max, onResize, onToggle }: {
 side: 'left' | 'right'; width: number; min: number; max: number;
 onResize: (width: number) => void; onToggle: () => void;
}) {
 const label = side === 'left' ? '导航栏' : '阅读器';
 const direction = side === 'left' ? 1 : -1;
 const update = (value: number) => onResize(Math.max(min, Math.min(max, value)));
 function start(event: PointerEvent<HTMLDivElement>) {
  if (event.button !== 0 || !event.isPrimary) return;
  const handle = event.currentTarget;
  const startX = event.clientX;
  const pointer = event.pointerId;
  handle.focus({preventScroll:true});
  handle.setPointerCapture(pointer);
  document.body.classList.add('resizing-panels');
  const move = (next: globalThis.PointerEvent) => {
   if (next.pointerId === pointer) update(width + (next.clientX - startX) * direction);
  };
  const finish = () => {
   handle.removeEventListener('pointermove', move);
   document.body.classList.remove('resizing-panels');
  };
  handle.addEventListener('pointermove',move);
  handle.addEventListener('lostpointercapture',finish,{once:true});
 }
 function keyboard(event: KeyboardEvent<HTMLDivElement>) {
  switch (event.key) {
   case 'ArrowLeft': update(width - 16 * direction); break;
   case 'ArrowRight': update(width + 16 * direction); break;
   case 'Home': update(min); break;
   case 'End': update(max); break;
   case 'Enter': onToggle(); break;
   default: return;
  }
  event.preventDefault();
 }
 return <div className={`panel-grip panel-grip-${side}`} role="separator" aria-orientation="vertical"
  aria-label={`调整${label}宽度`} aria-controls={side === 'left' ? 'navigation' : 'session-reader'}
  aria-valuenow={width} aria-valuemin={min} aria-valuemax={max} tabIndex={0}
  title={`拖动调整${label}宽度，双击收起`} onPointerDown={start} onKeyDown={keyboard} onDoubleClick={onToggle}/>;
}
