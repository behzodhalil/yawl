import { Suspense, lazy, useState } from "react";
import { ClipboardText, Table, Tabs } from "@cloudflare/kumo";
import { Ago, Card, Empty, Icon, Link, Loading, StatusBadge, useLoad } from "../ui";
import { plain } from "./settings/Audit";

const Command = lazy(() => import("../blocks/command"));

const READS = ["list_services", "service", "deploys", "deploy_log", "logs", "incidents"];
const CHANGES = ["deploy", "rollback"];

const SKILLS = [
  { value: "claude", label: "Claude Code", path: ".claude/skills/yawl/SKILL.md" },
  { value: "cursor", label: "Cursor", path: ".cursor/rules/yawl.mdc" },
  { value: "codex", label: "Codex", path: "AGENTS.md" },
];

const TEACHES = [
  <>Every <code>yawl.yaml</code> field and the rules a plan checks</>,
  "Read logs and the deploy log before changing code",
  "Deploy, follow it, and roll back when the app breaks",
  "Never print a secret value, never guess a server",
];

const TOKEN_ACTOR = /^API token (.+?)(?: \((.+)\))?$/;
const SHOWN = 10;

type AuditRow = { at: number; actor: string; text: string; kind: string };

function Code({ code }: { code: string }) {
  return (
    <Suspense fallback={<code className="block rounded-sm bg-kumo-recessed p-3 font-mono text-sm break-all ring ring-kumo-hairline">{code}</code>}>
      <Command code={code} />
    </Suspense>
  );
}

function Tools({ label, names }: { label: string; names: string[] }) {
  return (
    <div className="flex flex-col gap-2">
      <h3 className="text-sm font-medium">{label}</h3>
      <ul aria-label={label} className="flex flex-wrap gap-1.5">
        {names.map((n) => <li key={n}><code className="rounded-sm px-2 py-0.5 font-mono text-xs ring ring-kumo-line">{n}</code></li>)}
      </ul>
    </div>
  );
}

function Mcp() {
  return (
    <Card title="MCP server" className="h-full" actions={<StatusBadge kind="ok">In Yawl today</StatusBadge>}>
      <p className="text-kumo-subtle">The assistant uses the login <code className="text-kumo-default">yawl login</code> saved, so a Viewer's assistant reads and cannot deploy.</p>
      <Code code="claude mcp add yawl -- yawl mcp" />
      <Tools label="Reads" names={READS} />
      <Tools label="Changes" names={CHANGES} />
      <p className="text-sm text-kumo-subtle">It cannot read a variable's value, delete, restore or apply <code>yawl.yaml</code>.</p>
    </Card>
  );
}

function Skill() {
  const [agent, setAgent] = useState(SKILLS[0].value);
  const cur = SKILLS.find((s) => s.value === agent) ?? SKILLS[0];
  return (
    <Card title="Agent skill" className="h-full">
      <Tabs variant="segmented" className="self-start" aria-label="Agent" tabs={SKILLS.map(({ value, label }) => ({ value, label }))} value={agent} onValueChange={setAgent} />
      <Code code={`yawl skills add ${cur.value}`} />
      <p className="text-sm text-kumo-subtle">
        {cur.value === "codex" ? <>Adds a Yawl section to <code className="text-kumo-default">AGENTS.md</code> at the repository's root, and replaces it the next time.</>
          : <>Writes <code className="text-kumo-default">{cur.path}</code> into this repository.</>}
      </p>
      <div className="flex flex-col gap-2">
        <h3 className="text-sm font-medium">What it teaches the agent</h3>
        <ul className="flex flex-col gap-2">
          {TEACHES.map((t, i) => <li key={i} className="flex gap-2 [&_.ico]:mt-1 [&_.ico]:shrink-0 [&_.ico]:text-kumo-success"><Icon name="check" /><span>{t}</span></li>)}
        </ul>
      </div>
    </Card>
  );
}

function Docs() {
  const url = `${window.location.origin}/llms.txt`;
  return (
    <Card title="Docs for agents">
      <div className="flex flex-wrap items-center gap-3">
        <ClipboardText size="base" className="min-w-0 max-w-full" text={url} />
        <span className="text-kumo-subtle">The agent skill as plain text, served by this panel, so it matches the version you run.</span>
      </div>
    </Card>
  );
}

function Activity() {
  const audit = useLoad<{ rows: AuditRow[] }>("/api/audit?page=0&tokens=1");
  const members = useLoad<{ email: string; role: string }[]>("/api/members");
  const who = (email?: string) => {
    const role = members.data?.find((m) => m.email === email)?.role;
    return [email, role && role[0].toUpperCase() + role.slice(1)].filter(Boolean).join(" · ");
  };
  const rows = (audit.data?.rows ?? []).flatMap((r) => {
    const m = TOKEN_ACTOR.exec(r.actor);
    return m ? [{ ...r, token: m[1], email: m[2] }] : [];
  }).slice(0, SHOWN);
  return (
    <Card title="What agents did" actions={<Link to="/activity/audit" className="text-sm">All in Activity</Link>}>
      {!audit.data ? <Loading what={audit} /> : !rows.length ? <Empty text="No API token has changed anything yet." /> : (
        <Table>
          <Table.Header><Table.Row><Table.Head>When</Table.Head><Table.Head>Token</Table.Head><Table.Head>Did</Table.Head></Table.Row></Table.Header>
          <Table.Body>
            {rows.map((r, i) => (
              <Table.Row key={i}>
                <Table.Cell className="whitespace-nowrap text-kumo-subtle"><Ago ms={r.at} /></Table.Cell>
                <Table.Cell>{r.token}{who(r.email) && <span className="text-kumo-subtle"> · {who(r.email)}</span>}</Table.Cell>
                <Table.Cell>{plain(r)}</Table.Cell>
              </Table.Row>
            ))}
          </Table.Body>
        </Table>
      )}
    </Card>
  );
}

export function Agents() {
  return (
    <div className="flex flex-col gap-5">
      <p className="text-kumo-subtle">Let Claude Code, Cursor or Codex deploy and debug with Yawl. They act with your token's role, and every change lands in Activity.</p>
      <div className="grid gap-4 lg:grid-cols-2">
        <Mcp />
        <Skill />
      </div>
      <Docs />
      <Activity />
    </div>
  );
}
