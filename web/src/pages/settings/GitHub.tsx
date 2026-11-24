import { useState } from "react";
import { api } from "../../api";
import { Callout, errText, useBusy, useLoad } from "../../ui";

export function GitHub() {
  const [err, setErr] = useState("");
  const [finish, setFinish] = useState("");
  const gh = useLoad<{ connected: boolean; slug?: string; install_url?: string }>("/api/github");
  const [busy, run] = useBusy();
  const status = useLoad<{ github: string }>("/api/settings/commit-status");
  return (
    <div className="col w-form">
      <p className="muted">Yawl makes a GitHub App of its own for this panel. Install it on the repositories you deploy.</p>
      {gh.data?.connected && (
        <Callout tone="ok" title={`Connected as ${gh.data.slug}`}>
          Install the app on more repositories: <a href={gh.data.install_url} target="_blank" rel="noreferrer">{gh.data.install_url}</a>
        </Callout>
      )}
      {!gh.data?.connected && <div><button className="btn pri" disabled={busy} onClick={() => run(async () => {
        setErr(""); setFinish("");
        // Safari blocks a window.open made after an await.
        const tab = window.open("about:blank", "_blank");
        if (tab) tab.opener = null;
        try {
          const r = await api<{ url?: string; connected?: boolean; install_url?: string }>("POST", "/api/github/connect");
          if (!r.connected && r.url && tab) tab.location.href = r.url;
          else tab?.close();
          if (!r.connected && r.url && !tab) setFinish(r.url);
          if (r.connected) gh.reload();
        } catch (e) {
          tab?.close();
          setErr(errText(e));
        }
      })}>Connect GitHub</button></div>}
      {finish && <Callout tone="warn">The pop-up was blocked. <a href={finish} target="_blank" rel="noreferrer">Open GitHub to finish</a></Callout>}
      {err && <Callout tone="bad">{err}</Callout>}
      {status.data?.github && (
        <Callout tone="warn">{`Commit statuses were refused (${status.data.github}). Accept the app's new permission on GitHub, commit statuses: write.`}</Callout>
      )}
    </div>
  );
}
