import { Shell } from "../Shell";
import type { ProjectView } from "../types";
import { Pager, TableBar, useTable } from "../table";
import { Empty, EnvName, Icon, Link, Loading, useLoad } from "../ui";
import { KIND, firstEnv } from "./Project";

export function summary(p: ProjectView): string {
  const n = p.envs.reduce((a, e) => a + e.services.length, 0);
  return n === 0 ? "No services yet" : `${n} service${n === 1 ? "" : "s"}`;
}

export function kinds(p: ProjectView): [string, number][] {
  const count = new Map<string, number>();
  for (const s of p.envs.flatMap((e) => e.services)) count.set(s.kind, (count.get(s.kind) ?? 0) + 1);
  return [...count];
}

export function Projects() {
  const list = useLoad<ProjectView[]>("/api/projects");
  const tbl = useTable(list.data ?? [], { text: (p) => [p.name, ...p.envs.flatMap((e) => [e.name, ...e.services.map((s) => s.service)])].join(" "), sort: {} });
  return (
    <Shell crumb={["Projects"]} active="/">
      <div className="ph">
        <div><h1>Projects</h1><p className="sub">Each project has a production environment, and staging if you add it.</p></div>
        <Link to="/projects/new" className="btn pri"><Icon name="plus" />New project</Link>
      </div>
      {!list.data ? <Loading what={list} /> : list.data.length === 0 ? (
        <Empty text="No projects yet."><Link to="/projects/new" className="btn pri">New project</Link></Empty>
      ) : (
        <div className="col">
          <TableBar t={tbl} noun="projects" find="Search projects" />
          {tbl.total === 0 ? <Empty text="Nothing matches the search." /> : (
            <div className="grid3">
              {tbl.rows.map((p) => (
                <Link key={p.name} to={`/p/${p.name}/${firstEnv(p)}`} className="card pad col">
                  <h2>{p.name}</h2>
                  <span className="envs">{p.envs.map((e) => <EnvName key={e.name} env={e.name} />)}</span>
                  <span className="kinds">
                    {kinds(p).map(([kind, n]) => <span key={kind} className="kind" title={KIND[kind]?.[0] ?? kind}><Icon name={KIND[kind]?.[1] ?? "box"} />{n}</span>)}
                    <span className="muted">{summary(p)}</span>
                  </span>
                </Link>
              ))}
            </div>
          )}
          <Pager page={tbl.page} pages={tbl.pages} onPage={tbl.setPage} />
        </div>
      )}
    </Shell>
  );
}
