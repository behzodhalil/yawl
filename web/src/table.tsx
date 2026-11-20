import { useState, type ReactNode } from "react";
import { Icon } from "./ui";

type Key<T> = (r: T) => string | number;

export function useTable<T>(rows: T[], opts: { text: (r: T) => string; sort: Record<string, Key<T>>; size?: number }) {
  const size = opts.size ?? 20;
  const [q, setQ] = useState("");
  const [by, setBy] = useState<{ key: string; up: boolean } | null>(null);
  const [page, setPage] = useState(0);
  const t = q.trim().toLowerCase();
  const found = t ? rows.filter((r) => opts.text(r).toLowerCase().includes(t)) : [...rows];
  const key = by && opts.sort[by.key];
  if (key) found.sort((a, b) => { const x = key(a), y = key(b); return (x < y ? -1 : x > y ? 1 : 0) * (by.up ? 1 : -1); });
  const pages = Math.max(1, Math.ceil(found.length / size));
  const at = Math.min(page, pages - 1);
  return {
    q, search: (v: string) => { setQ(v); setPage(0); },
    by, sortBy: (key: string) => setBy((b) => (b?.key === key ? (b.up ? { key, up: false } : null) : { key, up: true })),
    rows: found.slice(at * size, at * size + size), total: found.length, page: at, pages, setPage,
  };
}
export type Table<T> = ReturnType<typeof useTable<T>>;

export function SortTh<T>({ t, k, children, right }: { t: Table<T>; k: string; children: ReactNode; right?: boolean }) {
  const on = t.by?.key === k;
  return (
    <th aria-sort={on ? (t.by!.up ? "ascending" : "descending") : "none"} data-u={right ? "right" : undefined}>
      <button type="button" className={"sorth" + (on ? " on" : "")} onClick={() => t.sortBy(k)}>
        {children}<Icon name={on && !t.by!.up ? "up" : "down"} />
      </button>
    </th>
  );
}

export function TableBar<T>({ t, noun, find }: { t: Table<T>; noun: string; find: string }) {
  return (
    <div className="tbar">
      <div className="col" style={{ gap: 0 }}><span className="dim">Total</span><b>{t.total} {noun}</b></div>
      <label className="tsearch"><Icon name="search" /><input type="search" aria-label={find} placeholder="Search" value={t.q} onChange={(e) => t.search(e.target.value)} /></label>
    </div>
  );
}

export function Pager({ page, pages, more, onPage }: { page: number; pages?: number; more?: boolean; onPage: (p: number) => void }) {
  const next = pages !== undefined ? page < pages - 1 : !!more;
  if (page === 0 && !next) return null;
  return (
    <div className="pager">
      <span className="dim">{pages !== undefined ? `Page ${page + 1} of ${pages}` : `Page ${page + 1}`}</span>
      <div className="row" style={{ gap: 8 }}>
        <button type="button" className="btn sm" disabled={page === 0} onClick={() => onPage(page - 1)}><Icon name="chev" />Previous</button>
        <button type="button" className="btn sm" disabled={!next} onClick={() => onPage(page + 1)}>Next<Icon name="chev" /></button>
      </div>
    </div>
  );
}
