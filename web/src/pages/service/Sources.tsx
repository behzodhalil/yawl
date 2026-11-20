import { useState } from "react";
import { api } from "../../api";
import { sealTo } from "../../seal";
import { Callout, Confirm, Field, Loading, errText, useLoad } from "../../ui";

type Src = { provider: string; url?: string; path?: string; workspace?: string; env?: string; updated_at: number };

export function Sources({ project, env }: { project: string; env: string }) {
  const base = `/api/environments/${project}/${env}`;
  const list = useLoad<Src[]>(`${base}/sources`);
  const [f, setF] = useState({ provider: "doppler", url: "", path: "", workspace: "", env: "", token: "" });
  const [err, setErr] = useState("");
  const [rm, setRm] = useState<string | null>(null);
  const up = (k: keyof typeof f) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) => setF({ ...f, [k]: e.target.value });
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
    <div className="card pad col">
      <h3>Secret managers</h3>
      <p className="muted">{`Every service in ${env} also gets the variables of these, read by the server at each start. A variable set here with the same name wins.`}</p>
      {!list.data ? <Loading what={list} /> : list.data.length > 0 && (
        <table className="tbl">
          <tbody>
            {list.data.map((s) => (
              <tr key={s.provider}><td>{s.provider}</td><td className="mono">{s.path || s.workspace || s.url || ""}</td>
                <td><button className="btn sm ghost" onClick={() => setRm(s.provider)}>Remove</button></td></tr>
            ))}
          </tbody>
        </table>
      )}
      <Field label="Manager">{(id) => (
        <select id={id} className="in" value={f.provider} onChange={up("provider")}>
          <option value="doppler">Doppler</option><option value="infisical">Infisical</option><option value="vault">HashiCorp Vault</option>
        </select>
      )}</Field>
      {f.provider !== "doppler" && <Field label="Address" hint={f.provider === "infisical" ? "Empty is Infisical's cloud" : "Like https://vault.acme.uz"}>{(id) => <input id={id} className="in mono" value={f.url} onChange={up("url")} />}</Field>}
      {f.provider !== "doppler" && <Field label="Path" hint={f.provider === "vault" ? "A KV secret, like secret/data/shop" : "A folder, like /api; empty is the root"}>{(id) => <input id={id} className="in mono" value={f.path} onChange={up("path")} />}</Field>}
      {f.provider === "infisical" && (
        <>
          <Field label="Project ID">{(id) => <input id={id} className="in mono" value={f.workspace} onChange={up("workspace")} />}</Field>
          <Field label="Environment" hint="Its slug, like prod">{(id) => <input id={id} className="in mono" value={f.env} onChange={up("env")} />}</Field>
        </>
      )}
      <Field label="Token" hint={f.provider === "doppler" ? "A service token of one config" : f.provider === "infisical" ? "clientId:clientSecret of a machine identity" : "A Vault token that may read the path"}>
        {(id) => <input id={id} className="in mono" type="password" autoComplete="off" value={f.token} onChange={up("token")} />}
      </Field>
      {err && <Callout tone="bad">{err}</Callout>}
      <div><button className="btn" disabled={!f.token.trim()} onClick={add}>Use it</button></div>
      {rm && (
        <Confirm title={`Stop using ${rm}?`} yes="Yes, stop using it" onYes={() => api("DELETE", `${base}/sources/${rm}`).then(list.reload)} onClose={() => setRm(null)}>
          <p>Every service in this environment loses its variables at the next start.</p>
        </Confirm>
      )}
    </div>
  );
}
