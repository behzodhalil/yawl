import { useState } from "react";
import { api } from "../../api";
import type { DomainView } from "../../types";
import { Callout, Field, Loading, errText, useBusy, useLoad } from "../../ui";

export function Panel() {
  const cur = useLoad<{ domain: string; hosts: DomainView[] }>("/api/settings/panel");
  if (!cur.data) return <Loading what={cur} />;
  return <PanelForm data={cur.data} reload={cur.reload} />;
}

function PanelForm({ data, reload }: { data: { domain: string; hosts: DomainView[] }; reload: () => void }) {
  const [domain, setDomain] = useState(data.domain);
  const [err, setErr] = useState("");
  const [saved, setSaved] = useState(false);
  const [busy, run] = useBusy();
  return (
    <div className="card pad col w-form">
      <h2>Panel</h2>
      {data.hosts.length === 0 ? <p className="muted">No server is marked as the panel's own yet. The installer marks it.</p> : (
        <div className="col" style={{ gap: 8 }}>
          {data.hosts.map((d) => (
            <div key={d.host} className="col" style={{ gap: 2 }}>
              <span className="row" style={{ gap: 8 }}><code>{d.host}</code><span className="tag">DNS {d.dns_state}</span><span className="tag">certificate {d.cert_state}</span></span>
              {d.dns_detail && <span style={{ color: "var(--warn)" }}>{d.dns_detail}</span>}
            </div>
          ))}
        </div>
      )}
      <Field label="Your domain for the panel" hint="Point an A record at the panel's server first. Once it works, it replaces the free host. Leave empty to use only the free host.">
        {(id) => <input id={id} className="in mono" placeholder="panel.acme.uz" value={domain} onChange={(e) => { setDomain(e.target.value.trim()); setSaved(false); }} />}
      </Field>
      {err && <Callout tone="bad">{err}</Callout>}
      <div className="row"><button className="btn pri" disabled={busy || domain === data.domain} onClick={() => run(async () => {
        setErr(""); setSaved(false);
        try { await api("PUT", "/api/settings/panel", { domain }); setSaved(true); reload(); } catch (e) { setErr(errText(e)); }
      })}>Save</button>{saved && <span className="muted">Saved</span>}</div>
    </div>
  );
}
