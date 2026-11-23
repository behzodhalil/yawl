import { useState } from "react";
import { api } from "../../api";
import { ago, utcStamp } from "../../format";
import { Callout, Confirm, Copy, Field, Icon, Loading, errText, useBusy, useLoad } from "../../ui";

type Member = { id: string; email: string; role: string; last_sign_in_at?: number; two_step: boolean };
type Invite = { id: string; role: string; expires_at: number; created_by?: string };

const ROLES = ["admin", "developer", "viewer"];

type Pending = { kind: "remove"; id: string } | { kind: "twoStepOff"; id: string } | { kind: "ownRole"; role: string } | null;

export function Members() {
  const members = useLoad<Member[]>("/api/members");
  const invites = useLoad<Invite[]>("/api/invites");
  const me = useLoad<{ email: string }>("/api/me");
  const two = useLoad<{ on: boolean; required: boolean }>("/api/account/two-step");
  const [role, setRole] = useState("developer");
  const [link, setLink] = useState("");
  const [inviting, setInviting] = useState(false);
  const [err, setErr] = useState("");
  const [inviteBusy, runInvite] = useBusy();
  const [sure, setSure] = useState<Pending>(null);
  const [ask, setAsk] = useState<{ title: string; yes: string; run: () => Promise<unknown> } | null>(null);
  const yes = (label: string, run: () => Promise<unknown>) => (
    <span className="row" style={{ gap: 8 }}>
      <button className="btn sm danger" onClick={() => (setSure(null), act(run))}>{label}</button>
      <button className="btn sm ghost" onClick={() => setSure(null)}>Cancel</button>
    </span>
  );
  const act = async (f: () => Promise<unknown>) => {
    setErr("");
    try {
      await f();
      members.reload();
      invites.reload();
      two.reload();
    } catch (e) {
      setErr(errText(e));
    }
  };
  const admins = members.data?.filter((m) => m.role === "admin").length ?? 0;
  if (members.error) return <Callout tone="warn">{members.error}</Callout>;
  return (
    <div className="col">
      <div className="row" style={{ justifyContent: "space-between" }}>
        <p className="muted">Admins do everything, developers deploy, viewers only look.</p>
        {!inviting && <button className="btn" onClick={() => setInviting(true)}><Icon name="plus" />Invite a member</button>}
      </div>
      {err && <Callout tone="bad">{err}</Callout>}
      {!members.data ? <Loading what={members} /> : (
        <div className="card scroll">
          <table className="tbl">
            <thead><tr><th>Member</th><th>Role</th><th>Last sign-in</th><th>Two-step</th><th /></tr></thead>
            <tbody>
              {members.data.map((m) => (
                <tr key={m.id}>
                  <td>{m.email}</td>
                  <td>
                    {m.role === "admin" && admins === 1 ? "Admin" : <select className="in" aria-label={`Role of ${m.email}`} value={m.role}
                      onChange={(e) => {
                        const r = e.target.value;
                        if (m.email === me.data?.email) return setSure({ kind: "ownRole", role: r });
                        setAsk({ title: `Make ${m.email} ${r}?`, yes: `Yes, make ${r}`, run: () => api("PUT", `/api/members/${m.id}`, { role: r }) });
                      }}>
                      {ROLES.map((r) => <option key={r} value={r}>{r}</option>)}
                    </select>}
                    {m.email === me.data?.email && sure?.kind === "ownRole" && yes(`Yes, make me ${sure.role}`, () => api("PUT", `/api/members/${m.id}`, { role: sure.role }))}
                  </td>
                  <td>{m.last_sign_in_at ? ago(m.last_sign_in_at) : "never"}</td>
                  <td>
                    <span className="row" style={{ gap: 8 }}>
                      {m.two_step ? "On" : "Off"}
                      {m.two_step && m.email !== me.data?.email && (sure?.kind === "twoStepOff" && sure.id === m.id ? yes("Yes, turn off", () => api("DELETE", `/api/members/${m.id}/two-step`)) : (
                        <button className="btn sm ghost" aria-label={`Turn off two-step sign in for ${m.email}`} onClick={() => setSure({ kind: "twoStepOff", id: m.id })}>Turn off</button>
                      ))}
                    </span>
                  </td>
                  <td>{m.role === "admin" && admins === 1 ? null : sure?.kind === "remove" && sure.id === m.id ? yes("Yes, remove", () => api("DELETE", `/api/members/${m.id}`)) : (
                    <button className="btn sm ghost" aria-label={`Remove ${m.email}`} onClick={() => setSure({ kind: "remove", id: m.id })}>Remove</button>
                  )}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {admins === 1 && <p className="muted">Invite another admin before you change your own role or leave the team.</p>}
      {two.data && (
        <div className="row">
          <span>{two.data.required ? "Two-step sign in is required for everyone." : "Two-step sign in is up to each member."}</span>
          <button className="btn sm" onClick={() => two.data!.required ? act(() => api("PUT", "/api/settings/two-step", { required: false })) : setAsk({ title: "Require two-step sign in for everyone?", yes: "Yes, require it", run: () => api("PUT", "/api/settings/two-step", { required: true }) })}>
            {two.data.required ? "Stop requiring it" : "Require two-step sign in for everyone"}
          </button>
        </div>
      )}
      {inviting && <div className="card pad col" style={{ maxWidth: 560 }}>
      <h3>Invite a member</h3>
      <Field label="Role of the new member" hint="A developer cannot reveal secrets, restore, delete, move or manage servers, but the code they deploy runs with the variables: give it only to people you trust with them.">{(id) => (
          <select id={id} className="in" value={role} onChange={(e) => setRole(e.target.value)}>
            {ROLES.map((r) => <option key={r} value={r}>{r}</option>)}
          </select>
        )}</Field>
      <div className="row">
        <button className="btn pri" disabled={inviteBusy} onClick={() => runInvite(() => act(async () => {
          const out = await api<{ token: string }>("POST", "/api/invites", { role });
          setLink(`${location.origin}/invite#${out.token}`);
        }))}>Invite</button>
        <button className="btn ghost" onClick={() => setInviting(false)}>Cancel</button>
      </div>
      </div>}
      {link && (
        <Callout tone="ok" title="Send this link to the new member. It works once, for 7 days, and is not shown again.">
          <span className="mono">{link}</span> <Copy text={link} />
        </Callout>
      )}
      {invites.data && invites.data.length > 0 && (
        <div className="col">
          <h3>Pending invites</h3>
          {invites.data.map((i) => (
            <div key={i.id} className="row" style={{ justifyContent: "space-between" }}>
              <span>{`${i.role}, valid until ${utcStamp(i.expires_at)}${i.created_by ? `, by ${i.created_by}` : ""}`}</span>
              <button className="btn sm ghost" onClick={() => setAsk({ title: "Revoke this invite?", yes: "Yes, revoke", run: () => api("DELETE", `/api/invites/${i.id}`) })}>Revoke</button>
            </div>
          ))}
        </div>
      )}
      {ask && <Confirm title={ask.title} yes={ask.yes} onClose={() => setAsk(null)} onYes={async () => { await ask.run(); members.reload(); invites.reload(); two.reload(); }} />}
    </div>
  );
}
