import { describe, expect, it, vi } from "vitest";
import { readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import sodium from "libsodium-wrappers";
import { backupKey, decodeRecovery, fingerprint, newRecoveryKey, openWith, reveal, sealTo, setShared, setVar } from "./seal";

const goVectors = JSON.parse(readFileSync(join(process.cwd(), "../internal/seal/testdata/vectors.json"), "utf8"));
const b64 = (u: Uint8Array) => sodium.to_base64(u, sodium.base64_variants.ORIGINAL);

describe("seal", () => {
  it("decodes the Go recovery key to the Go public key and opens the Go box", async () => {
    const { pub, priv } = await decodeRecovery(goVectors.recovery);
    await sodium.ready;
    expect(b64(pub)).toBe(goVectors.pub);
    expect(new TextDecoder().decode(await openWith(pub, priv, goVectors.box))).toBe("hello from go");
  });

  it("derives the Go backup signing key", async () => {
    expect(goVectors.recovery).toBe("yawl-recovery-XPLB4K453FMRWQIXU7RVNS3MWIQJMP3R2TM2UWDJ6BUWFSRJQOFA");
    expect(await backupKey(goVectors.recovery)).toBe("K1YH1GIfpg6mhGI3zpgJaYfyZf/LpaQuv+YzO8zvCQc=");
  });

  it("makes a key that round-trips through its own text", async () => {
    const k = await newRecoveryKey();
    expect(k.text).toMatch(/^yawl-recovery-[A-Z2-7]{52}$/);
    const { pub, priv } = await decodeRecovery("  " + k.text + "\n");
    expect(b64(pub)).toBe(k.pub);
    const box = await sealTo(k.pub, new TextEncoder().encode("x=y\n"));
    expect(new TextDecoder().decode(await openWith(pub, priv, box))).toBe("x=y\n");
    expect(fingerprint(k.pub)).toBe(k.pub.slice(0, 8));
  });

  it("writes the JS vectors for Go when asked", async () => {
    if (process.env.WRITE_VECTORS !== "1") return;
    const k = await newRecoveryKey();
    const box = await sealTo(k.pub, new TextEncoder().encode("hello from js"));
    writeFileSync(join(process.cwd(), "src/testdata/js-vectors.json"), JSON.stringify({ recovery: k.text, pub: k.pub, box }, null, 2) + "\n");
  });

  it("refuses a string that is not a key", async () => {
    await expect(decodeRecovery("yawl-recovery-ABC")).rejects.toThrow("That is not a Yawl recovery key.");
    await expect(decodeRecovery("hello")).rejects.toThrow("That is not a Yawl recovery key.");
  });

  it("never sends the value", async () => {
    await sodium.ready;
    const agent = sodium.crypto_box_keypair();
    const rec = sodium.crypto_box_keypair();
    const bodies: string[] = [];
    vi.stubGlobal("fetch", vi.fn(async (url: string, init?: RequestInit) => {
      if (init?.body) bodies.push(String(init.body));
      if (url.endsWith("/keys")) return new Response(JSON.stringify({ agent_pubkey: b64(agent.publicKey), recovery_pubkey: b64(rec.publicKey), server: "hel1" }));
      return new Response(null, { status: 204 });
    }));
    const value = "s3cr3t-value-123";
    await setVar("/api/services/acme/production/api", "API_KEY", value, true);
    expect(bodies).toHaveLength(1);
    const plain64 = btoa(value);
    for (const b of bodies) {
      expect(b).not.toContain(value);
      expect(b).not.toContain(plain64);
    }
    const sent = JSON.parse(bodies[0]);
    expect(sent.build).toBe(true);
    expect(new TextDecoder().decode(sodium.crypto_box_seal_open(sodium.from_base64(sent.agent_box, sodium.base64_variants.ORIGINAL), agent.publicKey, agent.privateKey))).toBe(value);
    expect(new TextDecoder().decode(sodium.crypto_box_seal_open(sodium.from_base64(sent.recovery_box, sodium.base64_variants.ORIGINAL), rec.publicKey, rec.privateKey))).toBe(value);
  });

  it("refuses to seal without a recovery key", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({ agent_pubkey: "x", recovery_pubkey: "", server: "hel1" }))));
    await expect(setVar("/api/environments/acme/production", "K", "v", false)).rejects.toThrow("Make the recovery key first, in Settings.");
  });

  it("refuses a value over 64 KB before sealing or sending it (yawl-6)", async () => {
    await sodium.ready;
    const agent = sodium.crypto_box_keypair();
    const bodies: string[] = [];
    vi.stubGlobal("fetch", vi.fn(async (url: string, init?: RequestInit) => {
      if (init?.body) bodies.push(String(init.body));
      if (url.endsWith("/keys")) return new Response(JSON.stringify({ agent_pubkey: b64(agent.publicKey), recovery_pubkey: b64(agent.publicKey), server: "hel1" }));
      if (url.endsWith("/shared-env/keys")) return new Response(JSON.stringify({ servers: [{ name: "hel1", agent_pubkey: b64(agent.publicKey) }], recovery_pubkey: b64(agent.publicKey) }));
      return new Response(null, { status: 204 });
    }));
    await expect(setVar("/api/services/acme/production/api", "K", "a".repeat(65537), false)).rejects.toThrow("A value is at most 64 KB.");
    await expect(setShared({ kind: "team" }, "K", "a".repeat(65537), false)).rejects.toThrow("A value is at most 64 KB.");
    expect(bodies).toHaveLength(0);
    await setVar("/api/services/acme/production/api", "K", "a".repeat(65536), false);
    expect(bodies).toHaveLength(1);
  });

  it("reveals through a one-time key", async () => {
    await sodium.ready;
    let to = "";
    vi.stubGlobal("fetch", vi.fn(async (_url: string, init?: RequestInit) => {
      to = JSON.parse(String(init!.body)).to;
      const box = sodium.crypto_box_seal(Uint8Array.from(new TextEncoder().encode("the value")), sodium.from_base64(to, sodium.base64_variants.ORIGINAL));
      return new Response(JSON.stringify({ box: b64(box) }));
    }));
    expect(await reveal("/api/services/acme/production/api", "API_KEY")).toBe("the value");
    expect(to).toHaveLength(44);
  });
});
