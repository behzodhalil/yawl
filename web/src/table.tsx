import { useState, type ReactNode } from "react";
import { InputGroup, Pagination, Table as KumoTable, cn } from "@cloudflare/kumo";
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
    <KumoTable.Head aria-sort={on ? (t.by!.up ? "ascending" : "descending") : "none"} className={right ? "text-right" : undefined}>
      <button type="button" className={cn("group inline-flex items-center gap-1", on && "text-kumo-default")} onClick={() => t.sortBy(k)}>
        {children}<span className={cn("inline-flex", !on && "opacity-0 group-hover:opacity-100")}><Icon name={on && !t.by!.up ? "up" : "down"} /></span>
      </button>
    </KumoTable.Head>
  );
}

export function TableBar<T>({ t, noun, find }: { t: Table<T>; noun: string; find: string }) {
  return (
    <div className="flex flex-wrap items-end justify-between gap-3">
      <div className="flex flex-col"><span className="text-kumo-subtle">Total</span><b className="font-medium">{t.total} {t.total === 1 ? noun.slice(0, -1) : noun}</b></div>
      <InputGroup className="w-full sm:w-60">
        <InputGroup.Addon><Icon name="search" /></InputGroup.Addon>
        <InputGroup.Input type="search" aria-label={find} placeholder="Search" value={t.q} onChange={(e) => t.search(e.target.value)} />
      </InputGroup>
    </div>
  );
}

export function Pager({ page, pages, more, onPage }: { page: number; pages?: number; more?: boolean; onPage: (p: number) => void }) {
  const next = pages !== undefined ? page < pages - 1 : !!more;
  if (page === 0 && !next) return null;
  const known = pages !== undefined ? { perPage: 1, totalCount: pages } : { hasNextPage: next };
  return (
    <Pagination page={page + 1} setPage={(p) => onPage(p - 1)} controls={pages !== undefined ? "full" : "simple"} {...known}
      text={() => <span className="text-kumo-subtle">{pages !== undefined ? `Page ${page + 1} of ${pages}` : `Page ${page + 1}`}</span>} />
  );
}
