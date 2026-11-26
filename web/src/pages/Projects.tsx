import { Grid, LayerCard } from "@cloudflare/kumo";
import { Shell } from "../Shell";
import type { ProjectView } from "../types";
import { Pager, TableBar, useTable } from "../table";
import { Empty, EnvName, Icon, Link, Loading, useLoad, ButtonLink } from "../ui";
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
      <div className="flex flex-wrap items-end justify-between gap-4">
        <div><h1>Projects</h1><p className="mt-1 text-kumo-subtle">New projects start with a production environment. Add more environments from the environment menu.</p></div>
        <ButtonLink to="/projects/new" variant="primary" icon={<Icon name="plus" />}>New project</ButtonLink>
      </div>
        {!list.data ? <Loading what={list} /> : list.data.length === 0 ? (
          <Empty text="No projects yet."><ButtonLink to="/projects/new" variant="primary">New project</ButtonLink></Empty>
        ) : (
          <div className="flex flex-col gap-3">
            <TableBar t={tbl} noun="projects" find="Search projects" />
            {tbl.total === 0 ? <Empty text="Nothing matches the search." /> : (
              <Grid variant="2up" gap="sm">
                {tbl.rows.map((p) => (
                  <LayerCard key={p.name} render={<Link to={`/p/${p.name}/${firstEnv(p)}`} />} className="flex flex-col gap-3 p-4 hover:ring-kumo-line">
                    <h2>{p.name}</h2>
                    <span className="flex flex-wrap gap-x-3.5 gap-y-1 font-mono text-sm text-kumo-subtle">{p.envs.map((e) => <EnvName key={e.name} env={e.name} />)}</span>
                    <span className="mt-0.5 flex flex-wrap items-center gap-x-3.5 gap-y-1.5 border-t border-kumo-hairline pt-2.5">
                      {kinds(p).map(([kind, n]) => <span key={kind} className="inline-flex items-center gap-1 tabular-nums text-kumo-subtle" title={KIND[kind]?.[0] ?? kind}><Icon name={KIND[kind]?.[1] ?? "box"} />{n}</span>)}
                      <span className="ml-auto text-kumo-subtle">{summary(p)}</span>
                    </span>
                  </LayerCard>
                ))}
              </Grid>
            )}
            <Pager page={tbl.page} pages={tbl.pages} onPage={tbl.setPage} />
          </div>
        )}
    </Shell>
  );
}
