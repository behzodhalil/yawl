import { useCallback, useEffect, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { HugeiconsIcon } from "@hugeicons/react";
import { BRANDS } from "./brands";
import { ICON_STROKE, ICONS, envIcon } from "./icons";
import { navigate } from "./router";
import { api } from "./api";

export function Icon({ name }: { name: string }) {
  const mark = BRANDS[name];
  if (mark) return <span className="ico brandmark" aria-hidden="true" dangerouslySetInnerHTML={{ __html: mark }} />;
  const icon = ICONS[name];
  return icon ? <HugeiconsIcon icon={icon} size={16} strokeWidth={ICON_STROKE} className="ico" aria-hidden="true" /> : null;
}

export function EnvName({ env }: { env: string }) {
  return <span className="envname"><Icon name={envIcon(env)} />{env}</span>;
}

export function Link({ to, children, className }: { to: string; children: ReactNode; className?: string }) {
  return (
    <a
      href={to}
      className={className}
      onClick={(e) => {
        if (e.metaKey || e.ctrlKey || e.shiftKey || e.button !== 0) return;
        e.preventDefault();
        navigate(to);
      }}
    >
      {children}
    </a>
  );
}

export type BadgeKind = "ok" | "busy" | "bad" | "warn" | "none" | "off";

export function StatusBadge({ kind, children }: { kind: BadgeKind; children: ReactNode }) {
  const word = typeof children === "string" ? (children.charAt(0).toUpperCase() + children.slice(1)).replaceAll("_", " ") : children;
  return <span className={"badge " + kind}><Icon name={"status-" + kind} />{word}</span>;
}

export function Callout({ tone, title, children }: { tone: "ok" | "warn" | "bad" | ""; title?: string; children?: ReactNode }) {
  return (
    <div className={"callout " + tone} role={tone === "bad" ? "alert" : undefined}>
      <div className="col" style={{ gap: 4, flex: 1 }}>
        {title && <span className="t">{tone && <Icon name={tone === "warn" ? "log-alert" : "status-" + tone} />}{title}</span>}
        {children && <span>{children}</span>}
      </div>
    </div>
  );
}

let fieldSeq = 0;

export function Field({ label, hint, children }: { label: string; hint?: ReactNode; children: (id: string) => ReactNode }) {
  const [id] = useState(() => "f" + ++fieldSeq);
  return (
    <div className="field">
      <label htmlFor={id}>{label}</label>
      {children(id)}
      {hint && <span className="hint">{hint}</span>}
    </div>
  );
}

const FOCUSABLE = "[autofocus], input, select, textarea, button, a[href]";

export function Modal({ title, children, onClose, dismissible = true }: { title: string; children: ReactNode; onClose: () => void; dismissible?: boolean }) {
  const box = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const was = document.activeElement as HTMLElement | null;
    const first = box.current?.querySelector<HTMLElement>("[autofocus]") ?? box.current?.querySelector<HTMLElement>(FOCUSABLE);
    first?.focus();
    return () => was?.focus();
  }, []);
  useEffect(() => {
    const key = (e: KeyboardEvent) => {
      if (e.key === "Escape" && dismissible) return onClose();
      if (e.key !== "Tab" || !box.current) return;
      const all = [...box.current.querySelectorAll<HTMLElement>(FOCUSABLE)].filter((el) => !(el as HTMLButtonElement).disabled);
      if (!all.length) return;
      const first = all[0], last = all[all.length - 1], at = document.activeElement;
      if (e.shiftKey && (at === first || !box.current.contains(at))) { e.preventDefault(); last.focus(); }
      else if (!e.shiftKey && (at === last || !box.current.contains(at))) { e.preventDefault(); first.focus(); }
    };
    window.addEventListener("keydown", key);
    return () => window.removeEventListener("keydown", key);
  }, [onClose, dismissible]);
  return (
    <div className="modal-bg" onClick={dismissible ? onClose : undefined}>
      <div className="modal" role="dialog" aria-modal="true" aria-label={title} ref={box} onClick={(e) => e.stopPropagation()}>
        <h2>{title}</h2>
        {children}
      </div>
    </div>
  );
}

export function useBusy(): [boolean, <T>(f: () => Promise<T>) => Promise<T | undefined>] {
  const [busy, setBusy] = useState(false);
  const on = useRef(false);
  const run = useCallback(async <T,>(f: () => Promise<T>) => {
    if (on.current) return undefined;
    on.current = true;
    setBusy(true);
    try {
      return await f();
    } finally {
      on.current = false;
      setBusy(false);
    }
  }, []);
  return [busy, run];
}

export function TestConnection({ path, label, hint }: { path: string; label?: string; hint?: string }) {
  const [busy, run] = useBusy();
  const [res, setRes] = useState<["ok" | "bad", string] | null>(null);
  const go = () => run(async () => {
    setRes(null);
    try {
      await api("POST", path);
      setRes(["ok", "Connected."]);
    } catch (e) {
      setRes(["bad", errText(e)]);
    }
  });
  return (
    <div className="col" style={{ alignItems: "flex-start" }}>
      <button type="button" className="btn" aria-label={label} disabled={busy} onClick={go}>{busy ? "Testing…" : "Test connection"}</button>
      {hint && <span className="muted">{hint}</span>}
      {res && <Callout tone={res[0]}>{res[1]}</Callout>}
    </div>
  );
}

export function useUnsaved(dirty: boolean): void {
  useEffect(() => {
    if (!dirty) return;
    const h = (e: BeforeUnloadEvent) => e.preventDefault();
    window.addEventListener("beforeunload", h);
    return () => window.removeEventListener("beforeunload", h);
  }, [dirty]);
}

export function Confirm({ title, yes, onYes, onClose, disabled, children }: { title: string; yes: string; onYes: () => Promise<unknown>; onClose: () => void; disabled?: boolean; children?: ReactNode }) {
  const [busy, run] = useBusy();
  const [err, setErr] = useState("");
  const go = () => run(async () => {
    setErr("");
    try {
      await onYes();
    } catch (e) {
      setErr(errText(e));
      return;
    }
    onClose();
  });
  return (
    <Modal title={title} onClose={onClose} dismissible={!busy}>
      {children}
      {err && <Callout tone="bad">{err}</Callout>}
      <div className="row" style={{ justifyContent: "flex-end" }}>
        <button className="btn ghost" type="button" disabled={busy} onClick={onClose}>Cancel</button>
        <button className="btn danger" type="button" disabled={busy || disabled} onClick={go}>{yes}</button>
      </div>
    </Modal>
  );
}

export function Copy({ text }: { text: string }) {
  const [done, setDone] = useState(false);
  return (
    <button
      className="btn sm"
      type="button"
      onClick={async () => {
        try {
          await navigator.clipboard.writeText(text);
          setDone(true);
          setTimeout(() => setDone(false), 1500);
        } catch {
          /* no clipboard: the text is on screen to select */
        }
      }}
    >
      <Icon name="copy" />
      {done ? "Copied" : "Copy"}
    </button>
  );
}

export type Load<T> = { data?: T; error?: string; reload: () => void };

export const POLL_MS = 5000;

export function useLoad<T>(path: string | null, every?: number): Load<T> {
  const [state, setState] = useState<{ data?: T; error?: string }>({});
  const [n, setN] = useState(0);
  useEffect(() => {
    if (!path) return;
    let live = true, flying = false;
    const get = () => {
      flying = true;
      return api<T>("GET", path).then(
        (data) => live && setState({ data }),
        (e: Error) => live && setState((s) => ({ data: s.data, error: e.message })),
      ).finally(() => { flying = false; });
    };
    get();
    const t = every && every > 0 ? setInterval(() => { if (!document.hidden && !flying) get(); }, every) : undefined;
    return () => {
      live = false;
      clearInterval(t);
    };
  }, [path, n, every]);
  const reload = useCallback(() => setN((x) => x + 1), []);
  return { ...state, reload };
}

export function useLiveLoad<T>(path: string | null, active: (d: T) => boolean): Load<T> {
  const [on, setOn] = useState(false);
  const l = useLoad<T>(path, on ? POLL_MS : undefined);
  const want = !!l.data && active(l.data);
  useEffect(() => setOn(want), [want]);
  return l;
}

export function Loading({ what }: { what: Load<unknown> }) {
  if (what.error) return <Callout tone="bad" title="Could not load this page">{what.error}</Callout>;
  return <Skeleton />;
}

export function Skeleton() {
  return (
    <div className="skel" role="status" aria-label="Loading">
      <i style={{ width: "40%" }} /><i /><i style={{ width: "85%" }} /><i style={{ width: "60%" }} />
    </div>
  );
}

export function errText(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}

export function Menu({ label, trigger, className, children }: { label: string; trigger: ReactNode; className?: string; children: ReactNode }) {
  const [open, setOpen] = useState(false);
  const [menuPos, setMenuPos] = useState<{ top?: string; bottom?: string; right: string } | null>(null);
  const box = useRef<HTMLDivElement>(null);
  const btn = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    if (!open) return;
    const away = (e: MouseEvent) => { if (!box.current?.contains(e.target as Node)) setOpen(false); };
    const esc = (e: KeyboardEvent) => { if (e.key === "Escape") { setOpen(false); btn.current?.focus(); } };
    const close = () => setOpen(false);
    document.addEventListener("mousedown", away);
    document.addEventListener("keydown", esc);
    window.addEventListener("scroll", close, true);
    window.addEventListener("resize", close);
    return () => {
      document.removeEventListener("mousedown", away);
      document.removeEventListener("keydown", esc);
      window.removeEventListener("scroll", close, true);
      window.removeEventListener("resize", close);
    };
  }, [open]);
  const menu = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    if (!open || !btn.current) {
      setMenuPos(null);
      return;
    }
    const rect = btn.current.getBoundingClientRect();
    const right = `${window.innerWidth - rect.right}px`;
    const up = rect.bottom + 6 + (menu.current?.offsetHeight ?? 0) > window.innerHeight;
    setMenuPos(up ? { bottom: `${window.innerHeight - rect.top + 6}px`, right } : { top: `${rect.bottom + 6}px`, right });
  }, [open]);
  return (
    <div className="dd" ref={box}>
      <button ref={btn} type="button" className={className ?? "btn ib"} aria-label={label} aria-haspopup="menu" aria-expanded={open} onClick={() => setOpen(!open)}>{trigger}</button>
      {open && <div className="dd-menu" ref={menu} role="menu" aria-label={label} onClick={() => { setOpen(false); btn.current?.focus(); }} style={{ position: "fixed", ...menuPos }}>{children}</div>}
    </div>
  );
}

export function MenuItem({ to, onClick, danger, children }: { to?: string; onClick?: () => void; danger?: boolean; children: ReactNode }) {
  const cls = danger ? "danger" : undefined;
  if (to) return <a role="menuitem" href={to} className={cls} onClick={(e) => { e.preventDefault(); navigate(to); }}>{children}</a>;
  return <button type="button" role="menuitem" className={cls} onClick={onClick}>{children}</button>;
}

export function Row({ title, hint, children }: { title: string; hint?: ReactNode; children: ReactNode }) {
  return (
    <section className="srow">
      <div><h2>{title}</h2>{hint && <p className="hint">{hint}</p>}</div>
      <div className="col">{children}</div>
    </section>
  );
}

export function Empty({ text, children }: { text: string; children?: ReactNode }) {
  return <div className="empty"><p>{text}</p>{children}</div>;
}
