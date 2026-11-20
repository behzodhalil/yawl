import { boot, recoveryText } from "./fake";

await boot();

const bar = document.createElement("div");
bar.className = "demo-bar";
bar.innerHTML = `<span><b>Demo.</b> Everything here lives in this tab and resets on reload. Moving an environment asks for the recovery key:</span>
  <code id="demo-key"></code><button id="demo-copy" class="btn">Copy key</button>`;
document.body.appendChild(bar);
document.getElementById("demo-key")!.textContent = recoveryText;
document.getElementById("demo-copy")!.addEventListener("click", async (e) => {
  const b = e.currentTarget as HTMLButtonElement;
  try { await navigator.clipboard.writeText(recoveryText); b.textContent = "Copied"; }
  catch { getSelection()?.selectAllChildren(document.getElementById("demo-key")!); b.textContent = "Selected; press Cmd+C"; }
});

await import("../src/main");
