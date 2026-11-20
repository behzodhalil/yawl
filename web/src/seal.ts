import sodium from "libsodium-wrappers";
import { api } from "./api";

const V = () => sodium.base64_variants.ORIGINAL;
const PREFIX = "yawl-recovery-";
const B32 = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";

function base32(b: Uint8Array): string {
  let out = "", bits = 0, acc = 0;
  for (const x of b) {
    acc = (acc << 8) | x;
    bits += 8;
    while (bits >= 5) out += B32[(acc >>> (bits -= 5)) & 31];
  }
  if (bits > 0) out += B32[(acc << (5 - bits)) & 31];
  return out;
}

function unbase32(s: string): Uint8Array | null {
  const out: number[] = [];
  let bits = 0, acc = 0;
  for (const c of s) {
    const v = B32.indexOf(c);
    if (v < 0) return null;
    acc = ((acc << 5) | v) & 0xfff;
    if ((bits += 5) >= 8) out.push((acc >>> (bits -= 8)) & 255);
  }
  return new Uint8Array(out);
}

const MAX_VALUE = 64 << 10;

export async function sealTo(pubB64: string, value: Uint8Array): Promise<string> {
  if (value.length > MAX_VALUE) throw new Error("A value is at most 64 KB.");
  await sodium.ready;
  // copied so the array is of this realm's Uint8Array, which libsodium checks for (jsdom's differs)
  return sodium.to_base64(sodium.crypto_box_seal(Uint8Array.from(value), sodium.from_base64(pubB64, V())), V());
}

export async function randomKey(): Promise<Uint8Array> {
  await sodium.ready;
  return sodium.randombytes_buf(32);
}

export async function openWith(pub: Uint8Array, priv: Uint8Array, boxB64: string): Promise<Uint8Array> {
  await sodium.ready;
  return sodium.crypto_box_seal_open(sodium.from_base64(boxB64, V()), pub, priv);
}

export async function newRecoveryKey(): Promise<{ text: string; pub: string }> {
  await sodium.ready;
  const k = sodium.crypto_box_keypair();
  return { text: PREFIX + base32(k.privateKey), pub: sodium.to_base64(k.publicKey, V()) };
}

export async function decodeRecovery(text: string): Promise<{ pub: Uint8Array; priv: Uint8Array }> {
  await sodium.ready;
  const t = text.trim();
  const priv = t.startsWith(PREFIX) ? unbase32(t.slice(PREFIX.length)) : null;
  if (!priv || priv.length !== 32) throw new Error("That is not a Yawl recovery key.");
  return { pub: sodium.crypto_scalarmult_base(priv), priv };
}

export async function backupKey(text: string): Promise<string> {
  const { priv } = await decodeRecovery(text);
  const ikm = await crypto.subtle.importKey("raw", Uint8Array.from(priv), "HKDF", false, ["deriveBits"]);
  const info = new TextEncoder().encode("yawl panel backup signing");
  const bits = await crypto.subtle.deriveBits({ name: "HKDF", hash: "SHA-256", salt: new Uint8Array(), info }, ikm, 256);
  return sodium.to_base64(new Uint8Array(bits), V());
}

export const fingerprint = (pubB64: string) => pubB64.slice(0, 8);

type Keys = { agent_pubkey: string; recovery_pubkey: string; server: string };

export async function setVar(scopePath: string, key: string, value: string, build: boolean): Promise<void> {
  const keys = await api<Keys>("GET", scopePath + "/keys");
  if (!keys.agent_pubkey) throw new Error(`Server ${keys.server} has no secrets key; join it again.`);
  if (!keys.recovery_pubkey) throw new Error("Make the recovery key first, in Settings.");
  const bytes = new TextEncoder().encode(value);
  const body = { agent_box: await sealTo(keys.agent_pubkey, bytes), recovery_box: await sealTo(keys.recovery_pubkey, bytes), build };
  await api("PUT", `${scopePath}/env/${encodeURIComponent(key)}`, body);
}

export type SharedScope = { kind: "team" } | { kind: "server"; name: string };

export async function setShared(where: SharedScope, key: string, value: string, build: boolean): Promise<void> {
  const keys = await api<{ servers: { name: string; agent_pubkey: string }[]; recovery_pubkey: string }>("GET", "/api/shared-env/keys");
  if (!keys.recovery_pubkey) throw new Error("Make the recovery key first, in Settings.");
  const bytes = new TextEncoder().encode(value);
  const recovery_box = await sealTo(keys.recovery_pubkey, bytes);
  const server = where.kind === "server" ? where.name : "";
  const to = server ? keys.servers.filter((s) => s.name === server) : keys.servers;
  const boxes: Record<string, string> = {};
  const sealed: Record<string, string> = {};
  for (const s of to) {
    if (!s.agent_pubkey) throw new Error(`Server ${s.name} has no secrets key; join it again.`);
    boxes[s.name] = await sealTo(s.agent_pubkey, bytes);
    sealed[s.name] = s.agent_pubkey;
  }
  const k = encodeURIComponent(key);
  if (!server) await api("PUT", `/api/shared-env/team/${k}`, { boxes, recovery_box, build, keys: sealed });
  else await api("PUT", `/api/shared-env/servers/${encodeURIComponent(server)}/${k}`, { agent_box: boxes[server], recovery_box, build, keys: sealed });
}

export async function setRef(scopePath: string, key: string, ref: string): Promise<void> {
  await api("PUT", `${scopePath}/env/${encodeURIComponent(key)}`, { ref });
}

export async function reveal(servicePath: string, key: string): Promise<string> {
  await sodium.ready;
  const k = sodium.crypto_box_keypair();
  const out = await api<{ box: string }>("POST", `${servicePath}/env/${encodeURIComponent(key)}/reveal`, { to: sodium.to_base64(k.publicKey, V()) });
  return new TextDecoder().decode(await openWith(k.publicKey, k.privateKey, out.box));
}

const SECRET_ALPHABET = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789";
const UNBIASED_BYTE_LIMIT = Math.floor(256 / SECRET_ALPHABET.length) * SECRET_ALPHABET.length;

export function randomSecret(): string {
  const out: string[] = [];
  const buf = new Uint8Array(1);
  while (out.length < 32) {
    crypto.getRandomValues(buf);
    if (buf[0] < UNBIASED_BYTE_LIMIT) out.push(SECRET_ALPHABET[buf[0] % SECRET_ALPHABET.length]);
  }
  return out.join("");
}
