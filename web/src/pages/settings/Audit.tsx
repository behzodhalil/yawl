import { useState } from "react";
import { stamp } from "../../DeleteDialog";
import { Field, Loading, useLoad } from "../../ui";
import { Pager } from "../../table";

type Row = { at: number; actor: string; text: string; kind: string };

function when(ms: number): string {
  const y = new Date(ms).getUTCFullYear();
  return y === new Date().getUTCFullYear() ? stamp(ms) : stamp(ms).replace(/^(\d+ \w+)/, `$1 ${y}`);
}

export function Audit() {
  const [who, setWho] = useState("");
  const [text, setText] = useState("");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [page, setPage] = useState(0);
  const params = new URLSearchParams();
  if (who) params.set("actor", who);
  if (text) params.set("q", text);
  if (from) params.set("from", String(Date.parse(from + "T00:00:00Z")));
  if (to) params.set("to", String(Date.parse(to + "T00:00:00Z") + 86_400_000));
  const filter = params.toString();
  const list = useLoad<{ rows: Row[]; more: boolean }>(`/api/audit?${filter}${filter ? "&" : ""}page=${page}`);
  const reset = (f: () => void) => (f(), setPage(0));
  return (
    <div className="card pad col" style={{ maxWidth: 820 }}>
      <h2>Audit log</h2>
      <p className="muted">Every change through the panel or the API, kept a year. Variable values are never recorded, only names.</p>
      <div className="row">
        <Field label="Who">{(id) => <input id={id} className="in" placeholder="owner or API token ci" value={who} onChange={(e) => reset(() => setWho(e.target.value))} />}</Field>
        <Field label="Text">{(id) => <input id={id} className="in" placeholder="variable, deployed…" value={text} onChange={(e) => reset(() => setText(e.target.value))} />}</Field>
        <Field label="From">{(id) => <input id={id} className="in" type="date" value={from} onChange={(e) => reset(() => setFrom(e.target.value))} />}</Field>
        <Field label="To">{(id) => <input id={id} className="in" type="date" value={to} onChange={(e) => reset(() => setTo(e.target.value))} />}</Field>
        <a className="btn" href={`/api/audit.csv${filter ? "?" + filter : ""}`}>Export CSV</a>
      </div>
      {!list.data ? <Loading what={list} /> : list.data.rows.length === 0 ? <p className="muted">Nothing matches.</p> : (
        <table className="tbl">
          <thead><tr><th>When</th><th>Who</th><th>What</th></tr></thead>
          <tbody>
            {list.data.rows.map((r, i) => (
              <tr key={r.at + ":" + i}><td className="mono dim">{when(r.at)}</td><td>{r.actor}</td><td>{r.text}</td></tr>
            ))}
          </tbody>
        </table>
      )}
      <Pager page={page} more={list.data?.more} onPage={setPage} />
    </div>
  );
}
