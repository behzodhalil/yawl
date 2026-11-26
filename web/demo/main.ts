import { buttonVariants } from "@cloudflare/kumo";
import { boot, recoveryText } from "./fake";

await boot();

const bar = document.createElement("div");
bar.className = "demo-bar";
bar.style.cssText = "position:fixed;left:0;right:0;bottom:0;display:flex;flex-wrap:wrap;align-items:center;gap:6px 12px;padding:8px 16px;font-size:13px;" +
  "background:var(--color-kumo-elevated);color:var(--text-color-kumo-default);border-top:1px solid var(--color-kumo-line)";
bar.innerHTML = `<span><b>Demo.</b> Everything here lives in this tab and resets on reload. Moving an environment asks for the recovery key:</span>
  <code id="demo-key" style="font-family:var(--font-mono);word-break:break-all"></code><button id="demo-copy" class="${buttonVariants({ size: "sm" })}">Copy key</button>`;
document.body.appendChild(bar);
const root = document.getElementById("root")!;
new ResizeObserver(() => { root.style.height = `calc(100% - ${bar.offsetHeight}px)`; }).observe(bar);
document.getElementById("demo-key")!.textContent = recoveryText;
document.getElementById("demo-copy")!.addEventListener("click", async (e) => {
  const b = e.currentTarget as HTMLButtonElement;
  try { await navigator.clipboard.writeText(recoveryText); b.textContent = "Copied"; }
  catch { getSelection()?.selectAllChildren(document.getElementById("demo-key")!); b.textContent = "Selected; press Cmd+C"; }
});

document.addEventListener("click", async (e) => {
  const a = (e.target as Element).closest?.<HTMLAnchorElement>('a[href^="/api/"]');
  if (!a) return;
  e.preventDefault();
  const tab = a.target === "_blank" ? window.open("", "_blank") : null;
  const href = URL.createObjectURL(await (await fetch(a.getAttribute("href")!)).blob());
  if (tab) tab.location.href = href;
  else Object.assign(document.createElement("a"), { href, download: a.pathname.split("/").pop() }).click();
}, true);

await import("../src/main");
