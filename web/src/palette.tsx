import { useEffect, useMemo, useRef, useState } from "react";
import { navigate } from "./router";
import { Icon, useLoad } from "./ui";
import type { ProjectView, ServerView } from "./types";

export type Place = { to: string; label: string; icon: string; hint?: string };

export function Palette({ pages, onClose }: { pages: Place[]; onClose: () => void }) {
  const projects = useLoad<ProjectView[]>("/api/projects");
  const servers = useLoad<ServerView[]>("/api/servers");
  const [q, setQ] = useState("");
  const [at, setAt] = useState(0);
  const list = useRef<HTMLUListElement>(null);

  const all = useMemo(() => {
    const out: Place[] = [...pages];
    for (const p of projects.data ?? []) {
      for (const e of p.envs) {
        out.push({ to: `/p/${p.name}/${e.name}`, label: `${p.name} / ${e.name}`, icon: "layers", hint: "Environment" });
        for (const s of e.services) out.push({ to: `/p/${p.name}/${e.name}/s/${s.service}`, label: s.service, icon: s.kind === "web" ? "globe" : s.kind, hint: `${p.name} / ${e.name}` });
      }
    }
    for (const s of servers.data ?? []) out.push({ to: `/servers/${encodeURIComponent(s.name)}`, label: s.name, icon: "server", hint: "Server" });
    return out;
  }, [pages, projects.data, servers.data]);

  const shown = useMemo(() => {
    const t = q.trim().toLowerCase();
    return t ? all.filter((x) => (x.label + " " + (x.hint ?? "")).toLowerCase().includes(t)) : all;
  }, [all, q]);
  useEffect(() => { setAt(0); }, [q]);
  useEffect(() => { list.current?.querySelector(".on")?.scrollIntoView?.({ block: "nearest" }); }, [at]);

  const err = projects.error ?? servers.error;
  const go = (x?: Place) => { if (x) { onClose(); navigate(x.to); } };
  return (
    <div className="modal-bg pal-bg" onClick={onClose}>
      <div className="modal pal" role="dialog" aria-modal="true" aria-label="Go to" onClick={(e) => e.stopPropagation()}>
        <div className="pal-in">
          <Icon name="search" />
          <input autoFocus role="combobox" aria-expanded="true" aria-controls="pal-list" aria-activedescendant={shown[at] ? "pal-" + at : undefined} aria-label="Go to" placeholder="Go to a page, project, service or server" value={q}
            onChange={(e) => setQ(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "ArrowDown") { e.preventDefault(); setAt((i) => Math.min(i + 1, shown.length - 1)); }
              else if (e.key === "ArrowUp") { e.preventDefault(); setAt((i) => Math.max(i - 1, 0)); }
              else if (e.key === "Enter") { e.preventDefault(); go(shown[at]); }
              else if (e.key === "Escape") onClose();
            }} />
          <span className="kbd">Esc</span>
        </div>
        <ul ref={list} id="pal-list" className="pal-list" role="listbox" aria-label="Places">
          {shown.length === 0 && <li className="pal-none">{(projects.data && servers.data) || err ? "Nothing matches." : "Looking…"}</li>}
          {shown.map((x, i) => (
            <li key={x.to} id={"pal-" + i} role="option" aria-selected={i === at} className={i === at ? "on" : ""}
              onMouseMove={() => setAt(i)} onClick={() => go(x)}>
              <Icon name={x.icon} /><span>{x.label}</span>{x.hint && <span className="dim">{x.hint}</span>}
            </li>
          ))}
        </ul>
        {err && <p className="pal-none" role="alert">{err}</p>}
      </div>
    </div>
  );
}
