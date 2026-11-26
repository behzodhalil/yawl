import { useState } from "react";
import { Badge, Button } from "@cloudflare/kumo";
import { api } from "../../api";
import type { DomainView } from "../../types";
import { Callout, Field, Loading, errText, useBusy, useLoad, Input, Card } from "../../ui";

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
    <Card title="Panel" className="max-w-[var(--w-form)]">
      {data.hosts.length === 0 ? <p className="text-kumo-subtle">No server is marked as the panel's own yet. The installer marks it.</p> : (
        <div className="flex flex-col gap-2">
          {data.hosts.map((d) => (
            <div key={d.host} className="flex flex-col gap-0.5">
              <span className="flex flex-wrap items-center gap-2"><code>{d.host}</code><Badge variant="outline">DNS {d.dns_state}</Badge><Badge variant="outline">certificate {d.cert_state}</Badge></span>
              {d.dns_detail && <span className="text-kumo-warning">{d.dns_detail}</span>}
            </div>
          ))}
        </div>
      )}
      <Field label="Your domain for the panel" hint="Point an A record at the panel's server first. Once it works, it replaces the free host. Leave empty to use only the free host.">
        {(id) => <Input id={id} className="font-mono text-sm" placeholder="panel.acme.uz" value={domain} onChange={(e) => { setDomain(e.target.value.trim()); setSaved(false); }} />}
      </Field>
      {err && <Callout tone="bad">{err}</Callout>}
      <div className="flex flex-wrap items-center gap-3 border-t border-kumo-hairline pt-4"><Button variant="primary" disabled={busy || domain === data.domain} onClick={() => run(async () => {
        setErr(""); setSaved(false);
        try { await api("PUT", "/api/settings/panel", { domain }); setSaved(true); reload(); } catch (e) { setErr(errText(e)); }
      })}>Save</Button>{saved && <span className="text-kumo-subtle">Saved</span>}</div>
    </Card>
  );
}
