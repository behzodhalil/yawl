import { useState } from "react";
import { api } from "../../api";
import { Callout, Confirm, Field, Loading, POLL_MS, StatusBadge, errText, useBusy, useLoad } from "../../ui";

type Rule = { id?: string; text: string; lines: number; minutes: number; open?: boolean };

export function LogAlerts({ project, env, service }: { project: string; env: string; service: string }) {
  const path = `/api/services/${project}/${env}/${service}/log-alerts`;
  const st = useLoad<{ rules: Rule[] }>(path, POLL_MS);
  const [text, setText] = useState("");
  const [lines, setLines] = useState(20);
  const [minutes, setMinutes] = useState(5);
  const [err, setErr] = useState("");

  const [busy, once] = useBusy();
  const [removing, setRemoving] = useState<Rule | null>(null);

  async function save(rules: Rule[]) {
    await api("PUT", path, { rules: rules.map(({ id, text, lines, minutes }) => ({ ...(id ? { id } : {}), text, lines, minutes })) });
    st.reload();
  }
  const add = () => once(async () => {
    setErr("");
    try {
      await save([...(st.data?.rules ?? []), { text: text.trim(), lines, minutes }]);
      setText("");
    } catch (e) {
      setErr(errText(e));
    }
  });

  if (!st.data) return <Loading what={st} />;
  const rules = st.data.rules;
  return (
    <section className="col">
      <h2>Alerts</h2>
      {rules.length > 0 && (
        <table className="tbl">
          <thead><tr><th>Lines</th><th>When</th><th>State</th><th /></tr></thead>
          <tbody>
            {rules.map((r, i) => (
              <tr key={r.id}>
                <td>{r.text ? `Lines with "${r.text}"` : "Stderr lines"}</td>
                <td>{`${r.lines ? `More than ${r.lines}` : "Any"} in ${r.minutes} minutes`}</td>
                <td>{r.open ? <StatusBadge kind="bad">Open</StatusBadge> : <StatusBadge kind="ok">Quiet</StatusBadge>}</td>
                <td><button className="btn sm" aria-label={`Remove alert ${i + 1}`} onClick={() => setRemoving(r)}>Remove</button></td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {rules.length < 5 && (
        <div className="row" style={{ alignItems: "flex-end", flexWrap: "wrap" }}>
          <Field label="Text" hint="Empty counts stderr lines.">
            {(id) => <input id={id} className="in" value={text} maxLength={200} onChange={(e) => setText(e.target.value)} />}
          </Field>
          <Field label="More than">
            {(id) => <input id={id} className="in" type="number" min={0} max={100000} value={lines} onChange={(e) => setLines(Number(e.target.value))} />}
          </Field>
          <Field label="Within, minutes">
            {(id) => <input id={id} className="in" type="number" min={1} max={60} value={minutes} onChange={(e) => setMinutes(Number(e.target.value))} />}
          </Field>
          <button className="btn" disabled={busy} onClick={add}>Add alert</button>
        </div>
      )}
      {err && <Callout tone="bad">{err}</Callout>}
      {removing && (
        <Confirm title="Remove this alert?" yes="Yes, remove" onClose={() => setRemoving(null)} onYes={() => save(rules.filter((x) => x.id !== removing.id))}>
          <p>{removing.text ? `Lines with "${removing.text}"` : "Stderr lines"} will stop sending messages.</p>
        </Confirm>
      )}
    </section>
  );
}
