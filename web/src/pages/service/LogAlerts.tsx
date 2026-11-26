import { useState } from "react";
import { Button, LayerCard, Table } from "@cloudflare/kumo";
import { api } from "../../api";
import { Callout, Confirm, Field, Loading, POLL_MS, StatusBadge, errText, useBusy, useLoad, Input } from "../../ui";

const TEXT_HINT = "log-alert-text-hint";

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
    <section className="flex flex-col gap-3">
      <h2>Alerts</h2>
      {rules.length > 0 && (
        <LayerCard className="overflow-x-auto p-0">
          <Table>
            <Table.Header><Table.Row><Table.Head>Lines</Table.Head><Table.Head>When</Table.Head><Table.Head>State</Table.Head><Table.Head /></Table.Row></Table.Header>
            <Table.Body>
              {rules.map((r, i) => (
                <Table.Row key={r.id}>
                  <Table.Cell>{r.text ? `Lines with "${r.text}"` : "Stderr lines"}</Table.Cell>
                  <Table.Cell>{`${r.lines ? `More than ${r.lines}` : "Any"} in ${r.minutes} minutes`}</Table.Cell>
                  <Table.Cell>{r.open ? <StatusBadge kind="bad">Open</StatusBadge> : <StatusBadge kind="ok">Quiet</StatusBadge>}</Table.Cell>
                  <Table.Cell><Button size="sm" aria-label={`Remove alert ${i + 1}`} onClick={() => setRemoving(r)}>Remove</Button></Table.Cell>
                </Table.Row>
              ))}
            </Table.Body>
          </Table>
        </LayerCard>
      )}
      {rules.length < 5 && (
        <div className="flex flex-wrap items-end gap-2.5">
          <Field label="Text">
            {(id) => <Input id={id} aria-describedby={TEXT_HINT} value={text} maxLength={200} onChange={(e) => setText(e.target.value)} />}
          </Field>
          <Field label="More than">
            {(id) => <Input id={id} type="number" min={0} max={100000} value={lines} onChange={(e) => setLines(Number(e.target.value))} />}
          </Field>
          <Field label="Within, minutes">
            {(id) => <Input id={id} type="number" min={1} max={60} value={minutes} onChange={(e) => setMinutes(Number(e.target.value))} />}
          </Field>
          <Button disabled={busy} onClick={add}>Add alert</Button>
        </div>
      )}
      {rules.length < 5 && <span id={TEXT_HINT} className="text-sm text-kumo-subtle">Empty text counts stderr lines.</span>}
      {err && <Callout tone="bad">{err}</Callout>}
      {removing && (
        <Confirm title="Remove this alert?" yes="Yes, remove" onClose={() => setRemoving(null)} onYes={() => save(rules.filter((x) => x.id !== removing.id))}>
          <p>{removing.text ? `Lines with "${removing.text}"` : "Stderr lines"} will stop sending messages.</p>
        </Confirm>
      )}
    </section>
  );
}
