import { useState } from "react";
import { api } from "../../api";
import { deployWord, shortRef, shortSha, took } from "../../format";
import { navigate } from "../../router";
import type { DeployRow } from "../../types";
import { Ago, Callout, Empty, Link, Loading, Modal, StatusBadge, errText, useBusy, useLoad } from "../../ui";

export function Events({ project, env, service }: { project: string; env: string; service: string }) {
  const path = `/api/services/${project}/${env}/${service}`;
  const list = useLoad<DeployRow[]>(path + "/deploys");
  const [err, setErr] = useState("");
  const [, run] = useBusy();
  const [back, setBack] = useState<DeployRow | null>(null);
  const [started, setStarted] = useState<{ url: string; note: string } | null>(null);
  const base = `/p/${project}/${env}/s/${service}`;
  const start = async (url: string, body: unknown) => {
    try {
      const d = await api<{ deploy_id: string; note?: string }>("POST", url, body);
      if (d.note) setStarted({ url: `${base}/deploys/${d.deploy_id}`, note: d.note });
      else navigate(`${base}/deploys/${d.deploy_id}`);
    } catch (e) {
      setErr(errText(e));
    }
  };
  const close = () => {
    if (started) navigate(started.url);
    setBack(null);
    setErr("");
  };
  const to = back ? shortRef(back) || back.subject || back.id : "";
  return (
    <div className="col">
      <h2>Deploys</h2>
      {!list.data ? <Loading what={list} /> : list.data.length === 0 ? <Empty text="No deploys yet. Deploy, above, starts the first." /> : (
        <div className="card scroll">
          <table className="tbl">
            <thead><tr><th>Commit</th><th>By</th><th data-u="right">Took</th><th data-u="right">Status</th></tr></thead>
            <tbody>
              {list.data.map((d) => {
                const [word, tone] = deployWord(d.state, d.live, d.auto_rolled_back, d.cause);
                return (
                  <tr key={d.id}>
                    <td>
                      <Link to={`${base}/deploys/${d.id}`} className="dlead">{d.subject || shortSha(d.commit) || "Branch head"}</Link>
                      <div className="dsub">{d.subject && shortRef(d) && <><span className="num">{shortRef(d)}</span> · </>}<Ago ms={d.created_at} />{d.restart ? " · restart" : d.rollback && " · rollback"}</div>
                    </td>
                    <td className="muted">{d.by}</td>
                    <td className="num muted" data-u="right">{took(d.took_ms)}</td>
                    <td data-u="right">
                      {d.can_roll_back ? <button className="linkbtn" onClick={() => { setErr(""); setBack(d); }}>Roll back</button> : <StatusBadge kind={tone}>{word}</StatusBadge>}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
      {back && (
        <Modal title={`Roll back ${service} to ${to}?`} onClose={close}>
          {started ? (
            <>
              <Callout tone="warn">{started.note}</Callout>
              <div className="row"><button className="btn pri" onClick={close}>Follow the rollback</button></div>
            </>
          ) : (
            <>
              <p>The database is not touched. Migrations that ran stay in place.</p>
              {err && <Callout tone="bad">{err}</Callout>}
              <div className="row">
                <button className="btn pri" onClick={() => run(() => start(path + "/rollback", { to: back.id }))}>Roll back to {to}</button>
                <button className="btn ghost" onClick={close}>Cancel</button>
              </div>
            </>
          )}
        </Modal>
      )}
    </div>
  );
}
