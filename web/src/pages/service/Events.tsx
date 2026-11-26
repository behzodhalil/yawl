import { Fragment, useState, type ReactNode } from "react";
import { Button, LayerCard, LayerDialog, Table, InlineCopyText } from "@cloudflare/kumo";
import { api } from "../../api";
import { actorName, deployWord, shortRef, shortSha, took } from "../../format";
import { Author, firstLine } from "./Deploy";
import { navigate } from "../../router";
import type { DeployRow } from "../../types";
import { Ago, Callout, Empty, Link, Loading, Modal, StatusBadge, errText, useBusy, useLoad } from "../../ui";

export function Events({ project, env, service }: { project: string; env: string; service: string }) {
  const path = `/api/services/${project}/${env}/${service}`;
  const list = useLoad<DeployRow[]>(path + "/deploys");
  const [back, setBack] = useState<DeployRow | null>(null);
  const base = `/p/${project}/${env}/s/${service}`;
  return (
    <div className="flex flex-col gap-3">
      <h2>Deploys</h2>
      {!list.data ? <Loading what={list} /> : list.data.length === 0 ? <Empty text="No deploys yet. Deploy, above, starts the first." /> : (
        <LayerCard className="overflow-x-auto p-0">
          <Table>
            <Table.Header><Table.Row><Table.Head>Commit</Table.Head><Table.Head className="text-right">Took</Table.Head><Table.Head className="text-right">Status</Table.Head><Table.Head><span className="sr-only">Action</span></Table.Head></Table.Row></Table.Header>
            <Table.Body>
              {list.data.map((d) => {
                const [word, tone] = deployWord(d.state, d.live, d.auto_rolled_back, d.cause);
                const meta = [
                  d.subject && shortRef(d) && <InlineCopyText value={d.digest ?? d.commit ?? shortRef(d)}>{shortRef(d)}</InlineCopyText>,
                  d.by && actorName(d.by),
                  <Ago ms={d.created_at} />,
                  d.restart ? "restart" : d.rollback && "rollback",
                ].filter(Boolean);
                return (
                  <Table.Row key={d.id}>
                    <Table.Cell>
                      <div className="flex min-w-0 items-center gap-3">
                        <Author by={d.by} />
                        <div className="min-w-0">
                          <Link to={`${base}/deploys/${d.id}`} className="block max-w-[60ch] truncate font-medium">{(d.subject && firstLine(d.subject)) || shortSha(d.commit) || "Branch head"}</Link>
                          <div className="mt-0.5 flex flex-wrap items-center gap-x-1.5 text-sm text-kumo-subtle">
                            {meta.map((m, i) => <Fragment key={i}>{i > 0 && <span aria-hidden="true">·</span>}<span>{m}</span></Fragment>)}
                          </div>
                        </div>
                      </div>
                    </Table.Cell>
                    <Table.Cell className="text-right font-mono text-sm tabular-nums text-kumo-subtle">{took(d.took_ms)}</Table.Cell>
                    <Table.Cell className="text-right"><StatusBadge kind={tone}>{word}</StatusBadge></Table.Cell>
                    <Table.Cell className="text-right">{d.can_roll_back && <Button variant="ghost" size="sm" onClick={() => setBack(d)}>Roll back</Button>}</Table.Cell>
                  </Table.Row>
                );
              })}
            </Table.Body>
          </Table>
        </LayerCard>
      )}
      {back && (
        <RollbackDialog path={path} base={base} service={service} to={back} onClose={() => setBack(null)}>
          <p>The database is not touched. Migrations that ran stay in place.</p>
        </RollbackDialog>
      )}
    </div>
  );
}

export function RollbackDialog({ path, base, service, to, onClose, children }: { path: string; base: string; service: string; to?: DeployRow; onClose: () => void; children: ReactNode }) {
  const [busy, run] = useBusy();
  const [err, setErr] = useState("");
  const [started, setStarted] = useState<{ url: string; note: string } | null>(null);
  const target = to ? shortRef(to) || to.subject || to.id : "the previous version";
  const follow = (url: string) => { navigate(url); onClose(); };
  const go = () => run(async () => {
    setErr("");
    try {
      const d = await api<{ deploy_id: string; note?: string }>("POST", path + "/rollback", to ? { to: to.id } : {});
      const url = `${base}/deploys/${d.deploy_id}`;
      if (d.note) setStarted({ url, note: d.note });
      else follow(url);
    } catch (e) {
      setErr(errText(e));
    }
  });
  return (
    <Modal title={`Roll back ${service} to ${target}?`} onClose={started ? () => follow(started.url) : onClose} dismissible={!busy} actions={
      started
        ? <LayerDialog.Actions><LayerDialog.Action onClick={() => follow(started.url)}>Follow the rollback</LayerDialog.Action></LayerDialog.Actions>
        : <LayerDialog.Actions dismissLabel="Cancel"><LayerDialog.Action variant="destructive" disabled={busy} onClick={go}>{`Roll back to ${target}`}</LayerDialog.Action></LayerDialog.Actions>
    }>
      {started ? <Callout tone="warn">{started.note}</Callout> : children}
      {err && <Callout tone="bad">{err}</Callout>}
    </Modal>
  );
}
