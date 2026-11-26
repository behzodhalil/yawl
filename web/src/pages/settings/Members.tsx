import { useState } from "react";
import { Button, LayerCard, Radio, Select, Table } from "@cloudflare/kumo";
import { api } from "../../api";
import { dateTime } from "../../format";
import { Ago, Callout, Confirm, Copy, Icon, Loading, errText, useBusy, useLoad, useReveal, Card } from "../../ui";

type Member = { id: string; email: string; role: string; last_sign_in_at?: number; two_step: boolean };
type Invite = { id: string; role: string; expires_at: number; created_by?: string };

const ROLES: Record<string, string> = { admin: "Admin", developer: "Developer", viewer: "Viewer" };

const ROLE_HINTS: Record<string, string> = {
  admin: "An admin does everything: members, servers, secrets, restores and deletes.",
  developer: "A developer cannot reveal secrets, restore, delete, move or manage servers, but the code they deploy runs with the variables: give it only to people you trust with them.",
  viewer: "A viewer sees projects, logs and metrics, and cannot deploy or change anything.",
};

const DANGER_GHOST = "text-kumo-danger";

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
  const form = useReveal<HTMLDivElement>(inviting);
  const made = useReveal<HTMLDivElement>(link);
  const [sure, setSure] = useState<Pending>(null);
  const [ask, setAsk] = useState<{ title: string; yes: string; run: () => Promise<unknown> } | null>(null);
  const yes = (label: string, run: () => Promise<unknown>) => (
    <span className="flex flex-wrap items-center gap-2">
      <Button size="sm" variant="destructive" onClick={() => (setSure(null), act(run))}>{label}</Button>
      <Button size="sm" variant="ghost" onClick={() => setSure(null)}>Cancel</Button>
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
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center justify-between gap-2.5">
        <p className="text-kumo-subtle">Admins do everything, developers deploy, viewers only look.</p>
        {!inviting && <Button variant="primary" icon={<Icon name="plus" />} onClick={() => (setLink(""), setInviting(true))}>Invite a member</Button>}
      </div>
      {err && <Callout tone="bad">{err}</Callout>}
      {!members.data ? <Loading what={members} /> : (
        <LayerCard className="overflow-x-auto p-0">
          <Table>
            <Table.Header><Table.Row><Table.Head>Member</Table.Head><Table.Head>Role</Table.Head><Table.Head>Last sign-in</Table.Head><Table.Head>Two-step</Table.Head><Table.Head /></Table.Row></Table.Header>
            <Table.Body>
              {members.data.map((m) => (
                <Table.Row key={m.id}>
                  <Table.Cell>{m.email}</Table.Cell>
                  <Table.Cell>
                    {m.role === "admin" && admins === 1 ? ROLES.admin : <Select size="sm" aria-label={`Role of ${m.email}`} className="min-w-32" value={m.role} items={ROLES}
                      onValueChange={(v) => {
                        const r = String(v);
                        if (m.email === me.data?.email) return setSure({ kind: "ownRole", role: r });
                        setAsk({ title: `Make ${m.email} ${r}?`, yes: `Yes, make ${r}`, run: () => api("PUT", `/api/members/${m.id}`, { role: r }) });
                      }} />}
                    {m.email === me.data?.email && sure?.kind === "ownRole" && yes(`Yes, make me ${sure.role}`, () => api("PUT", `/api/members/${m.id}`, { role: sure.role }))}
                  </Table.Cell>
                  <Table.Cell><Ago ms={m.last_sign_in_at} /></Table.Cell>
                  <Table.Cell>
                    <span className="flex flex-wrap items-center gap-2">
                      {m.two_step ? "On" : "Off"}
                      {m.two_step && m.email !== me.data?.email && (sure?.kind === "twoStepOff" && sure.id === m.id ? yes("Yes, turn off", () => api("DELETE", `/api/members/${m.id}/two-step`)) : (
                        <Button size="sm" variant="ghost" className={DANGER_GHOST} aria-label={`Turn off two-step sign-in for ${m.email}`} onClick={() => setSure({ kind: "twoStepOff", id: m.id })}>Turn off</Button>
                      ))}
                    </span>
                  </Table.Cell>
                  <Table.Cell>{m.role === "admin" && admins === 1 ? null : sure?.kind === "remove" && sure.id === m.id ? yes("Yes, remove", () => api("DELETE", `/api/members/${m.id}`)) : (
                    <Button size="sm" variant="ghost" className={DANGER_GHOST} aria-label={`Remove ${m.email}`} onClick={() => setSure({ kind: "remove", id: m.id })}>Remove</Button>
                  )}</Table.Cell>
                </Table.Row>
              ))}
            </Table.Body>
          </Table>
        </LayerCard>
      )}
      {admins === 1 && <p className="text-kumo-subtle">You are the only admin. Invite another admin before you change your role or leave the team.</p>}
      {two.data && (
        <div className="flex flex-wrap items-center gap-2.5">
          <span>{two.data.required ? "Two-step sign-in is required for everyone." : "Two-step sign-in is up to each member."}</span>
          <Button size="sm" onClick={() => two.data!.required ? act(() => api("PUT", "/api/settings/two-step", { required: false })) : setAsk({ title: "Require two-step sign-in for everyone?", yes: "Yes, require it", run: () => api("PUT", "/api/settings/two-step", { required: true }) })}>
            {two.data.required ? "Stop requiring it" : "Require two-step sign-in for everyone"}
          </Button>
        </div>
      )}
      {inviting && (
        <div ref={form} className="max-w-[560px]">
        <Card title="Invite a member" level={3}>
          <Radio.Group legend="Role of the new member" description={ROLE_HINTS[role]} value={role} onValueChange={(v) => setRole(String(v))}>
            {Object.entries(ROLES).map(([r, label]) => <Radio.Item key={r} label={label} value={r} />)}
          </Radio.Group>
          <div className="flex flex-wrap items-center gap-3 border-t border-kumo-hairline pt-4">
            <Button variant="primary" disabled={inviteBusy} onClick={() => runInvite(() => act(async () => {
              const out = await api<{ token: string }>("POST", "/api/invites", { role });
              setLink(`${location.origin}/invite#${out.token}`);
              setInviting(false);
            }))}>Invite</Button>
            <Button variant="ghost" onClick={() => setInviting(false)}>Cancel</Button>
          </div>
        </Card>
        </div>
      )}
      {link && (
        <div ref={made} tabIndex={-1}>
        <Callout tone="ok" title="Send this link to the new member. It works once, for 7 days, and is not shown again.">
          <span className="font-mono text-sm">{link}</span> <Copy text={link} />
        </Callout>
        </div>
      )}
      {invites.data && invites.data.length > 0 && (
        <div className="flex flex-col gap-3">
          <h3>Pending invites</h3>
          <LayerCard className="overflow-x-auto p-0">
            <Table>
              <Table.Header><Table.Row><Table.Head>Role</Table.Head><Table.Head>Valid until</Table.Head><Table.Head>Invited by</Table.Head><Table.Head /></Table.Row></Table.Header>
              <Table.Body>
                {invites.data.map((i) => (
                  <Table.Row key={i.id}>
                    <Table.Cell>{ROLES[i.role] ?? i.role}</Table.Cell>
                    <Table.Cell>{dateTime(i.expires_at)}</Table.Cell>
                    <Table.Cell>{i.created_by ?? ""}</Table.Cell>
                    <Table.Cell><Button size="sm" variant="ghost" className={DANGER_GHOST} onClick={() => setAsk({ title: "Revoke this invite?", yes: "Yes, revoke", run: () => api("DELETE", `/api/invites/${i.id}`) })}>Revoke</Button></Table.Cell>
                  </Table.Row>
                ))}
              </Table.Body>
            </Table>
          </LayerCard>
        </div>
      )}
      {ask && <Confirm title={ask.title} yes={ask.yes} onClose={() => setAsk(null)} onYes={async () => { await ask.run(); members.reload(); invites.reload(); two.reload(); }} />}
    </div>
  );
}
