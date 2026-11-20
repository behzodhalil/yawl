import { describe, expect, it } from "vitest";
import sodium from "libsodium-wrappers";
import { newRecoveryKey, sealTo } from "./seal";
import { reseal } from "./reseal";
import type { MovePlan } from "./types";

const V = () => sodium.base64_variants.ORIGINAL;
const enc = (s: string) => new TextEncoder().encode(s);

async function plan(restore: boolean) {
  await sodium.ready;
  const rec = await newRecoveryKey();
  const target = sodium.crypto_box_keypair();
  const p: MovePlan = {
    lines: [], restore, recovery_pubkey: rec.pub, to_pubkey: sodium.to_base64(target.publicKey, V()),
    vars: [
      { id: "var_a", key: "TOKEN", service: "api", recovery_box: await sealTo(rec.pub, enc("tok-1")), updated_at: 11 },
      { id: "var_b", key: "SHARED", service: "", recovery_box: await sealTo(rec.pub, enc("shared-2")), updated_at: 12 },
    ],
  };
  const open = (b: string) => new TextDecoder().decode(sodium.crypto_box_seal_open(sodium.from_base64(b, V()), target.publicKey, target.privateKey));
  return { p, text: rec.text, open };
}

describe("reseal", () => {
  it("reseals every var to the new agent", async () => {
    const { p, text, open } = await plan(false);
    const out = await reseal(p, text);
    expect(open(out.boxes.var_a.agent_box)).toBe("tok-1");
    expect(open(out.boxes.var_b.agent_box)).toBe("shared-2");
    expect(out.boxes.var_a.updated_at).toBe(11);
    expect(out.recovery_box).toBe("");
  });

  it("refuses another recovery key", async () => {
    const { p } = await plan(false);
    const other = await newRecoveryKey();
    await expect(reseal(p, other.text)).rejects.toThrow("This is not this panel's recovery key.");
  });

  it("seals the recovery key for a restore", async () => {
    const { p, text, open } = await plan(true);
    const out = await reseal(p, "  " + text + "\n");
    expect(open(out.recovery_box)).toBe(text);
  });
});
