import { forwardRef, useCallback, useEffect, useId, useRef, useState, type AnchorHTMLAttributes, type ReactNode } from "react";
import { Badge, Banner, Button, DropdownMenu, Empty as KumoEmpty, Input as KumoInput, Label, LayerCard, LayerDialog, LinkButton, LinkProvider, SkeletonLine, createKumoToastManager, labelVariants, type BadgeVariant, type InputProps, type LinkButtonProps } from "@cloudflare/kumo";
import type { IconWeight } from "@phosphor-icons/react";
import { BRANDS } from "./brands";
import { ICONS, envIcon } from "./icons";
import { navigate } from "./router";
import { api } from "./api";
import { ago, dateTime } from "./format";

export function Icon({ name, weight }: { name: string; weight?: IconWeight }) {
  const mark = BRANDS[name];
  if (mark) return <span className="ico brandmark" aria-hidden="true" dangerouslySetInnerHTML={{ __html: mark }} />;
  const C = ICONS[name];
  return C ? <C size={16} weight={weight} className="ico" aria-hidden="true" /> : null;
}

export function EnvName({ env }: { env: string }) {
  return <span className="inline-flex items-center gap-1.5"><Icon name={envIcon(env)} />{env}</span>;
}

type AnchorProps = AnchorHTMLAttributes<HTMLAnchorElement> & { to?: string };

export const Link = forwardRef<HTMLAnchorElement, AnchorProps>(function Link({ to, href, onClick, ...rest }, ref) {
  const target = to ?? href ?? "";
  return (
    <a
      ref={ref}
      href={target}
      {...rest}
      onClick={(e) => {
        onClick?.(e);
        if (e.defaultPrevented || rest.target || e.metaKey || e.ctrlKey || e.shiftKey || e.button !== 0 || !target.startsWith("/")) return;
        e.preventDefault();
        navigate(target);
      }}
    />
  );
});

export const toasts = createKumoToastManager();

export function notify(title: string, variant: "success" | "error" | "warning" | "info" | "default" = "success", description?: ReactNode) {
  toasts.add({ title, description, variant });
}

export function ButtonLink({ to, ...props }: Omit<LinkButtonProps, "href"> & { to: string }) {
  return <LinkProvider component={Link}><LinkButton href={to} {...props} /></LinkProvider>;
}

const EDGE_FADE_PX = 16;

function edgeFade(hiddenBefore: boolean, hiddenAfter: boolean): string | undefined {
  if (!hiddenBefore && !hiddenAfter) return undefined;
  const at = (hidden: boolean) => (hidden ? "transparent" : "black");
  return `linear-gradient(to right, ${at(hiddenBefore)}, black ${EDGE_FADE_PX}px, black calc(100% - ${EDGE_FADE_PX}px), ${at(hiddenAfter)})`;
}

export function NavTabs({ label, tabs }: { label: string; tabs: { to: string; label: ReactNode; current: boolean }[] }) {
  const strip = useRef<HTMLElement>(null);
  const [hidden, setHidden] = useState<[boolean, boolean]>([false, false]);
  const current = tabs.find((t) => t.current)?.to;
  const measure = useCallback(() => {
    const el = strip.current;
    if (el) setHidden([el.scrollLeft > 0, el.scrollLeft + el.clientWidth < el.scrollWidth - 1]);
  }, []);
  useEffect(() => {
    strip.current?.querySelector("[aria-current=page]")?.scrollIntoView?.({ inline: "nearest", block: "nearest" });
    measure();
    window.addEventListener("resize", measure);
    return () => window.removeEventListener("resize", measure);
  }, [current, measure]);
  const fade = edgeFade(...hidden);
  return (
    <nav ref={strip} aria-label={label} onScroll={measure} style={fade ? { maskImage: fade } : undefined}
      className="flex h-9 max-w-full shrink-0 scroll-px-4 items-stretch self-start overflow-x-auto rounded-lg bg-kumo-recessed px-0.5 ring ring-kumo-hairline/70">
      {tabs.map((t) => (
        <Link key={t.to} to={t.to} aria-current={t.current ? "page" : undefined}
          className="my-0.5 flex items-center rounded-md px-2.5 font-medium whitespace-nowrap text-kumo-subtle hover:text-kumo-default focus-visible:ring-2 focus-visible:ring-kumo-brand focus-visible:outline-hidden aria-[current=page]:bg-kumo-base aria-[current=page]:text-kumo-default aria-[current=page]:shadow-sm aria-[current=page]:ring aria-[current=page]:ring-kumo-line">
          {t.label}
        </Link>
      ))}
    </nav>
  );
}

export function Card({ title, level = 2, actions, className, children }: { title: ReactNode; level?: 2 | 3; actions?: ReactNode; className?: string; children: ReactNode }) {
  const Heading = level === 2 ? "h2" : "h3";
  return (
    <LayerCard className={className}>
      <LayerCard.Secondary className="flex min-h-10 flex-wrap items-center justify-between gap-3">
        <Heading className="flex items-center gap-2 text-base font-medium">{title}</Heading>
        {actions}
      </LayerCard.Secondary>
      <LayerCard.Primary className="flex flex-col gap-4">{children}</LayerCard.Primary>
    </LayerCard>
  );
}

export type BadgeKind = "ok" | "busy" | "bad" | "warn" | "none" | "off";

export function NoSignal({ lastSeen }: { lastSeen: number | undefined }) {
  return <StatusBadge kind="none">No signal · <Ago ms={lastSeen} /></StatusBadge>;
}

export function Ago({ ms }: { ms: number | undefined }) {
  if (!ms) return ago(ms);
  return <time dateTime={new Date(ms).toISOString()} title={dateTime(ms)}>{ago(ms)}</time>;
}

const BADGE: Record<BadgeKind, BadgeVariant> = { ok: "success", bad: "error", warn: "warning", busy: "info", none: "neutral", off: "secondary" };

export function StatusBadge({ kind, children }: { kind: BadgeKind; children: ReactNode }) {
  const word = typeof children === "string" ? (children.charAt(0).toUpperCase() + children.slice(1)).replaceAll("_", " ") : children;
  return <Badge variant={BADGE[kind]} icon={<Icon name={"status-" + kind} />} className={"badge " + kind}>{word}</Badge>;
}

const BANNER = { ok: "default", warn: "alert", bad: "error", "": "secondary" } as const;

export function Callout({ tone, title, children }: { tone: "ok" | "warn" | "bad" | ""; title?: string; children?: ReactNode }) {
  return (
    <Banner variant={BANNER[tone]} className={"callout " + tone} role={tone === "bad" ? "alert" : undefined}
      icon={tone ? <Icon name={tone === "warn" ? "log-alert" : "status-" + tone} /> : undefined}
      title={title} description={children} />
  );
}

let fieldSeq = 0;
const labelId = (fieldId: string) => fieldId + "-label";
const hintId = (fieldId: string) => fieldId + "-hint";

export const Input = forwardRef<HTMLInputElement, InputProps>(function Input(props, ref) {
  const named = props.label || props["aria-label"] || props["aria-labelledby"];
  return <KumoInput ref={ref} aria-labelledby={!named && props.id ? labelId(props.id) : undefined} {...props} />;
});

export function Field({ label, hint, children }: { label: string; hint?: ReactNode; children: (id: string) => ReactNode }) {
  const [id] = useState(() => "f" + ++fieldSeq);
  const hinted = !!hint;
  useEffect(() => {
    const control = document.getElementById(id);
    if (hinted) control?.setAttribute("aria-describedby", hintId(id));
    else if (control?.getAttribute("aria-describedby") === hintId(id)) control.removeAttribute("aria-describedby");
  });
  return (
    <div className="field flex flex-col gap-1.5">
      <label htmlFor={id} id={labelId(id)} className={labelVariants()}><Label asContent>{label}</Label></label>
      {children(id)}
      {hint && <span id={hintId(id)} className="hint text-sm text-kumo-subtle">{hint}</span>}
    </div>
  );
}

type DialogSize = "sm" | "base" | "lg" | "xl";

function openerOf(el: Element | null): HTMLElement | null {
  const menu = el?.closest("[role=menu]");
  const trigger = menu?.id ? document.querySelector<HTMLElement>(`[aria-controls="${menu.id}"]`) : null;
  return trigger ?? (el instanceof HTMLElement ? el : null);
}

const FIRST_FIELD = "[autofocus], input:not([type=hidden]), select, textarea, button, a[href]";

export function Modal({ title, size, children, actions, onClose, dismissible = true }: {
  title: string; size?: DialogSize; children: ReactNode; actions?: ReactNode; onClose: () => void; dismissible?: boolean;
}) {
  const [opener] = useState(() => openerOf(document.activeElement));
  const body = useRef<HTMLDivElement>(null);
  useEffect(() => () => {
    requestAnimationFrame(() => { if (opener?.isConnected && document.activeElement === document.body) opener.focus(); });
  }, [opener]);
  useEffect(() => {
    const frame = requestAnimationFrame(() => body.current?.querySelector<HTMLElement>(FIRST_FIELD)?.focus());
    return () => cancelAnimationFrame(frame);
  }, []);
  return (
    <LayerDialog.Root open dismissDisabled={!dismissible} onOpenChange={(open: boolean) => { if (!open && dismissible) onClose(); }}>
      <LayerDialog.Content size={size} closeLabel="Close">
        <LayerDialog.Title>{title}</LayerDialog.Title>
        <LayerDialog.Body><div ref={body} className="flex flex-col gap-4">{children}</div></LayerDialog.Body>
        {actions}
      </LayerDialog.Content>
    </LayerDialog.Root>
  );
}

const FORM_FIELD = "input:not([type=hidden]), select, textarea";

export function useReveal<T extends HTMLElement>(key: unknown) {
  const ref = useRef<T>(null);
  const last = useRef(key);
  useEffect(() => {
    if (key === last.current) return;
    last.current = key;
    const el = ref.current;
    if (!key || !el) return;
    el.scrollIntoView?.({ block: "nearest" });
    (el.querySelector<HTMLElement>(FORM_FIELD) ?? (el.hasAttribute("tabindex") ? el : null))?.focus({ preventScroll: true });
  }, [key]);
  return ref;
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
    <div className="flex flex-col items-start gap-3">
      <Button aria-label={label} disabled={busy} onClick={go}>{busy ? "Testing…" : "Test connection"}</Button>
      {hint && <span className="text-kumo-subtle">{hint}</span>}
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
    <Modal title={title} onClose={onClose} dismissible={!busy} actions={
      <LayerDialog.Actions dismissLabel="Cancel">
        <LayerDialog.Action variant="destructive" disabled={busy || disabled} onClick={go}>{yes}</LayerDialog.Action>
      </LayerDialog.Actions>
    }>
      {children}
      {err && <Callout tone="bad">{err}</Callout>}
    </Modal>
  );
}

export function Copy({ text }: { text: string }) {
  const [done, setDone] = useState(false);
  return (
    <Button
      size="sm"
      icon={<Icon name={done ? "check" : "copy"} />}
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
      {done ? "Copied" : "Copy"}
    </Button>
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
    <div className="flex max-w-[720px] flex-col gap-3 py-1" role="status" aria-label="Loading">
      <SkeletonLine blockHeight={20} minWidth={30} maxWidth={45} />
      <SkeletonLine minWidth={85} maxWidth={100} />
      <SkeletonLine minWidth={70} maxWidth={90} />
      <SkeletonLine minWidth={45} maxWidth={65} />
    </div>
  );
}

export function errText(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}

export function Menu({ label, trigger, variant = "secondary", size = "base", wide = false, alignStart = false, children }: {
  label: string; trigger: ReactNode; variant?: "primary" | "secondary" | "ghost"; size?: "sm" | "base"; wide?: boolean; alignStart?: boolean; children: ReactNode;
}) {
  return (
    <DropdownMenu>
      <DropdownMenu.Trigger render={wide
        ? <Button variant={variant} size={size} aria-label={label}>{trigger}</Button>
        : <Button variant={variant} size={size} shape="square" title={label} aria-label={label}>{trigger}</Button>} />
      <DropdownMenu.Content align={alignStart ? "start" : "end"}>{children}</DropdownMenu.Content>
    </DropdownMenu>
  );
}

export function MenuItem({ to, onClick, danger, current, icon, description, children }: {
  to?: string; onClick?: () => void; danger?: boolean; current?: boolean; icon?: string; description?: ReactNode; children: ReactNode;
}) {
  const id = useId();
  const variant = danger ? "danger" : "default";
  const named = description ? { "aria-labelledby": id + "t", "aria-describedby": id + "d" } : {};
  const body = description ? (
    <span className="flex min-w-0 flex-col py-0.5">
      <span id={id + "t"} className="font-medium">{children}</span>
      <span id={id + "d"} className="text-xs text-kumo-subtle">{description}</span>
    </span>
  ) : children;
  const glyph = icon ? <Icon name={icon} /> : undefined;
  if (to) {
    return (
      <DropdownMenu.LinkItem render={<Link to={to} />} variant={variant} icon={glyph} aria-current={current ? "true" : undefined} {...named}>
        {body}{current && <span className="ml-auto inline-flex"><Icon name="check" /></span>}
      </DropdownMenu.LinkItem>
    );
  }
  return <DropdownMenu.Item onClick={onClick} variant={variant} icon={glyph} selected={current} {...named}>{body}</DropdownMenu.Item>;
}

export function Row({ title, hint, children }: { title: string; hint?: ReactNode; children: ReactNode }) {
  return (
    <section className="grid gap-3 border-t border-kumo-hairline py-6 first-of-type:border-t-0 first-of-type:pt-1 md:grid-cols-[minmax(200px,300px)_minmax(0,1fr)] md:gap-10">
      <div><h2 className="text-base font-semibold">{title}</h2>{hint && <p className="mt-1 text-sm text-kumo-subtle">{hint}</p>}</div>
      <div className="flex max-w-[480px] flex-col gap-3">{children}</div>
    </section>
  );
}

export function Empty({ text, children }: { text: string; children?: ReactNode }) {
  return <KumoEmpty title={text} contents={children} className="rounded-lg border border-dashed border-kumo-line" />;
}
