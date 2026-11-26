import { useState } from "react";
import { Button, Select, SensitiveInput, Table } from "@cloudflare/kumo";
import { api } from "../../api";
import { sealTo } from "../../seal";
import { Callout, Confirm, Field, Loading, errText, useLoad, Input, Card } from "../../ui";

type Src = { provider: string; url?: string; path?: string; workspace?: string; env?: string; updated_at: number };

export function Sources({ project, env }: { project: string; env: string }) {
  const base = `/api/environments/${project}/${env}`;
  const list = useLoad<Src[]>(`${base}/sources`);
  const [f, setF] = useState({ provider: "doppler", url: "", path: "", workspace: "", env: "", token: "" });
  const [err, setErr] = useState("");
  const [rm, setRm] = useState<string | null>(null);
  const up = (k: keyof typeof f) => (e: React.ChangeEvent<HTMLInputElement>) => setF({ ...f, [k]: e.target.value });
  async function add() {
    setErr("");
    try {
      const keys = await api<{ agent_pubkey: string; recovery_pubkey: string; server: string }>("GET", `${base}/keys`);
      if (!keys.agent_pubkey) throw new Error(`Server ${keys.server} has no secrets key; join it again.`);
      if (!keys.recovery_pubkey) throw new Error("Make the recovery key first, in Settings.");
      const bytes = new TextEncoder().encode(f.token.trim());
      const { token, provider, ...rest } = f;
      void token;
      await api("PUT", `${base}/sources/${provider}`, { ...rest, agent_box: await sealTo(keys.agent_pubkey, bytes), recovery_box: await sealTo(keys.recovery_pubkey, bytes) });
      setF({ ...f, token: "" });
      list.reload();
    } catch (e) {
      setErr(errText(e));
    }
  }
  return (
    <Card title="Secret managers" level={3} className="max-w-[var(--w-form)]">
      <p className="text-kumo-subtle">{`Every service in ${env} also gets the variables of these, read by the server at each start. A variable set here with the same name wins.`}</p>
      {!list.data ? <Loading what={list} /> : list.data.length > 0 && (
        <Table>
          <Table.Body>
            {list.data.map((s) => (
              <Table.Row key={s.provider}><Table.Cell>{s.provider}</Table.Cell><Table.Cell className="font-mono text-sm">{s.path || s.workspace || s.url || ""}</Table.Cell>
                <Table.Cell><Button variant="ghost" size="sm" onClick={() => setRm(s.provider)}>Remove</Button></Table.Cell></Table.Row>
            ))}
          </Table.Body>
        </Table>
      )}
      <Field label="Manager">{(id) => (
        <Select aria-labelledby={id + "-label"} value={f.provider} onValueChange={(v) => setF({ ...f, provider: String(v) })}
          items={{ doppler: "Doppler", infisical: "Infisical", vault: "HashiCorp Vault" }} />
      )}</Field>
      {f.provider !== "doppler" && <Field label="Address" hint={f.provider === "infisical" ? "Empty is Infisical's cloud" : "Like https://vault.acme.uz"}>{(id) => <Input id={id} className="font-mono" value={f.url} onChange={up("url")} />}</Field>}
      {f.provider !== "doppler" && <Field label="Path" hint={f.provider === "vault" ? "A KV secret, like secret/data/shop" : "A folder, like /api; empty is the root"}>{(id) => <Input id={id} className="font-mono" value={f.path} onChange={up("path")} />}</Field>}
      {f.provider === "infisical" && (
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="Project ID">{(id) => <Input id={id} className="font-mono" value={f.workspace} onChange={up("workspace")} />}</Field>
          <Field label="Environment" hint="Its slug, like prod">{(id) => <Input id={id} className="font-mono" value={f.env} onChange={up("env")} />}</Field>
        </div>
      )}
      <Field label="Token" hint={f.provider === "doppler" ? "A service token of one config" : f.provider === "infisical" ? "clientId:clientSecret of a machine identity" : "A Vault token that may read the path"}>
        {(id) => <SensitiveInput id={id} aria-labelledby={id + "-label"} className="font-mono" autoComplete="off" value={f.token} onValueChange={(token) => setF({ ...f, token })} />}
      </Field>
      {err && <Callout tone="bad">{err}</Callout>}
      <div className="flex flex-wrap items-center gap-3 border-t border-kumo-hairline pt-4"><Button disabled={!f.token.trim()} onClick={add}>Use it</Button></div>
      {rm && (
        <Confirm title={`Stop using ${rm}?`} yes="Yes, stop using it" onYes={() => api("DELETE", `${base}/sources/${rm}`).then(list.reload)} onClose={() => setRm(null)}>
          <p>Every service in this environment loses its variables at the next start.</p>
        </Confirm>
      )}
    </Card>
  );
}
