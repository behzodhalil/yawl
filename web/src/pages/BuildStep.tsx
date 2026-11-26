import { lazy, Suspense } from "react";
import { Badge, Button, Grid, LayerCard, Loader, Radio, cn } from "@cloudflare/kumo";
import type { InspectData, ServiceForm, Steps } from "../types";
import { Callout, Card, Field, Input } from "../ui";

const YamlView = lazy(() => import("./YamlView"));

export type Render = "server" | "static";
export type BuildChoice = { framework: string; render: Render };
type Preset = { id: string; label: string; mark: string; dep: string; render: Render; start: string; port: number; output: string };

export const PRESETS: Preset[] = [
  { id: "nextjs", label: "Next.js", mark: "N", dep: "next", render: "server", start: "npm start", port: 3000, output: "out" },
  { id: "nuxt", label: "Nuxt", mark: "Nu", dep: "nuxt", render: "server", start: "node .output/server/index.mjs", port: 3000, output: ".output/public" },
  { id: "sveltekit", label: "SvelteKit", mark: "Sk", dep: "@sveltejs/kit", render: "server", start: "node build", port: 3000, output: "build" },
  { id: "astro", label: "Astro", mark: "As", dep: "astro", render: "static", start: "node dist/server/entry.mjs", port: 4321, output: "dist" },
  { id: "react-router", label: "React Router", mark: "Rr", dep: "@react-router/dev", render: "server", start: "npm start", port: 3000, output: "build/client" },
  { id: "vite", label: "Vite", mark: "Vi", dep: "vite", render: "static", start: "npm run preview", port: 4173, output: "dist" },
  { id: "angular", label: "Angular", mark: "Ng", dep: "@angular/core", render: "static", start: "node dist/server/server.mjs", port: 4000, output: "dist/browser" },
  { id: "static", label: "Static HTML", mark: "</>", dep: "", render: "static", start: "", port: 0, output: "" },
  { id: "other", label: "Other", mark: "?", dep: "", render: "server", start: "", port: 0, output: "" },
];

const presetOf = (id: string) => PRESETS.find((p) => p.id === id);
const STEP_KEYS = ["install", "build", "start", "output"] as const;

export function choiceFor(id: string): BuildChoice {
  return { framework: presetOf(id)?.id ?? "", render: presetOf(id)?.render ?? "server" };
}

export const detectedChoice = (d: InspectData): BuildChoice => ({ framework: presetOf(d.framework ?? "")?.id ?? "", render: d.static ? "static" : "server" });

export function stepsFor(c: BuildChoice, steps: Steps = {}): Steps {
  const p = presetOf(c.framework);
  if (c.render === "server") return { ...steps, output: "" };
  return { ...steps, start: "", output: steps.output || p?.output || "" };
}

export function yamlOf(form: ServiceForm): { code: string; fresh: number[] } {
  const lines: [string, boolean][] = [["services:", false], [`  - name: ${form.name || "app"}`, false], ["    kind: web", false], [`    repo: ${form.repo}`, false], [`    port: ${form.port}`, false]];
  if (form.root) lines.push([`    root: ${form.root}`, false]);
  if (form.builder === "railpack") lines.push(["    builder: railpack", false]);
  const set = STEP_KEYS.filter((k) => form.steps?.[k]);
  if (set.length) lines.push(["    steps:", true], ...set.map((k): [string, boolean] => [`      ${k}: ${form.steps![k]}`, true]));
  return { code: lines.map(([l]) => l).join("\n") + "\n", fresh: lines.flatMap(([, n], i) => (n ? [i + 1] : [])) };
}

function found(d: InspectData): string {
  const p = presetOf(d.framework ?? "");
  if (!p) return "No framework found. Pick one for its defaults, or leave it to the builder.";
  if (p.id === "static") return "Found index.html and no package.json, so it is served as static files.";
  return `Found ${p.dep} in package.json, so it builds as ${p.label}. Fill in a command only to change what the build runs.`;
}

function builderNote(d: InspectData, form: ServiceForm): string {
  if (form.builder === "railpack") return "Builds with Railpack.";
  if (d.language) return `Yawl writes the Dockerfile for ${d.language}.`;
  return "Builds with Railpack.";
}

export function BuildStep({ form, set, inspected, choice, setChoice }: {
  form: ServiceForm; set: (f: ServiceForm) => void; inspected: InspectData; choice: BuildChoice; setChoice: (c: BuildChoice) => void;
}) {
  const preset = presetOf(choice.framework);
  const node = !!preset?.dep || !!inspected.framework;
  const steps = form.steps ?? {};
  const up = (p: Steps) => set({ ...form, steps: { ...steps, ...p } });
  const render = (r: Render) => {
    const c = { ...choice, render: r };
    setChoice(c);
    set({ ...form, steps: stepsFor(c, steps) });
  };
  const pick = (p: Preset) => {
    const c = choiceFor(p.id);
    setChoice(c);
    set({ ...form, builder: p.id === "other" ? "railpack" : "", port: p.port || form.port, steps: p.id === "static" ? {} : stepsFor(c, { install: steps.install, build: steps.build }) });
  };
  const own = !!inspected.dockerfile && form.builder !== "railpack";
  const yaml = yamlOf(form);
  return (
    <div className="grid items-start gap-4 lg:grid-cols-[minmax(0,1fr)_380px]">
      <Card title="How to build it">
        {own ? (
          <Callout tone="">
            <span className="flex flex-wrap items-center justify-between gap-2.5">
              Builds with your Dockerfile. Root folder and port still apply.
              <Button size="sm" onClick={() => pick(presetOf("other")!)}>Build with Railpack instead</Button>
            </span>
          </Callout>
        ) : <>
        <p className="text-kumo-subtle">{found(inspected)}</p>
        <div role="group" aria-label="Framework" className="flex flex-col gap-1.5">
          <span className="font-medium">Framework</span>
          <Grid variant="3up" gap="sm">
            {PRESETS.map((p) => (
              <LayerCard key={p.id} render={<button type="button" aria-pressed={choice.framework === p.id} onClick={() => pick(p)} />}
                className={cn("flex cursor-pointer items-center gap-2.5 p-3 text-left hover:ring-kumo-line", choice.framework === p.id && "bg-kumo-tint ring-2 ring-kumo-brand")}>
                <span aria-hidden className="grid size-8 shrink-0 place-items-center rounded-xs bg-kumo-recessed font-mono text-sm font-semibold">{p.mark}</span>
                <span className="flex min-w-0 flex-col">
                  <b className="font-semibold">{p.label}</b>
                  {p.id === "other" && <span className="text-sm text-kumo-subtle">Railpack decides</span>}
                </span>
                {inspected.framework === p.id && <Badge variant="success" className="ml-auto">Detected</Badge>}
              </LayerCard>
            ))}
          </Grid>
        </div>
        </>}
        {!own && choice.framework !== "static" && (
          <>
            <Radio.Group legend="How it runs" appearance="card" orientation="horizontal" value={choice.render} onValueChange={(v: string) => render(v as Render)}>
              <Radio.Item label="Server" value="server" description="Runs a start command and answers each request. Needed for API routes and server rendering." />
              <Radio.Item label="Static" value="static" description="Builds files once and serves them from Caddy. No process, almost no memory." />
            </Radio.Group>
            <div className="grid grid-cols-[repeat(auto-fit,minmax(240px,1fr))] gap-4">
              <Field label="Install command" hint="Empty: the build picks it from your lock file.">{(id) => <Input id={id} className="font-mono" placeholder={node ? "npm ci" : "picked by the build"} value={steps.install ?? ""} onChange={(e) => up({ install: e.target.value })} />}</Field>
              <Field label="Build command" hint="Empty: the build script in package.json, if any.">{(id) => <Input id={id} className="font-mono" placeholder={node ? "npm run build" : "picked by the build"} value={steps.build ?? ""} onChange={(e) => up({ build: e.target.value })} />}</Field>
              {choice.render === "server" && <Field label="Start command" hint={inspected.static ? "Needed: without it this site is still served as static files." : "Empty: the start script, or a Procfile's web: line."}>{(id) => <Input id={id} className="font-mono" placeholder={preset?.start || "picked by the build"} value={steps.start ?? ""} onChange={(e) => up({ start: e.target.value })} />}</Field>}
              {choice.render === "static" && <Field label="Output folder" hint="The folder the build writes, served as the site.">{(id) => <Input id={id} className="font-mono" placeholder="dist" value={steps.output ?? ""} onChange={(e) => up({ output: e.target.value.trim() })} />}</Field>}
            </div>
          </>
        )}
        <Field label="Root folder" hint="Empty builds the whole repository.">{(id) => <Input id={id} className="font-mono" placeholder="the whole repository" value={form.root ?? ""} onChange={(e) => set({ ...form, root: e.target.value.trim() })} />}</Field>
        {!own && <p className="text-sm text-kumo-subtle">{builderNote(inspected, form)}</p>}
      </Card>
      <Card title="As yawl.yaml">
        <Suspense fallback={<Loader />}><YamlView code={yaml.code} highlightLines={yaml.fresh} /></Suspense>
        <p className="text-sm text-kumo-subtle">Highlighted lines are what you changed from the builder's own choice.</p>
      </Card>
    </div>
  );
}
