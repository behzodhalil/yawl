import sodium from "libsodium-wrappers";
import { decodeRecovery, openWith, sealTo } from "./seal";
import type { MovePlan } from "./types";

export async function reseal(plan: MovePlan, recoveryText: string): Promise<{
  boxes: Record<string, { agent_box: string; updated_at: number }>;
  recovery_box: string;
}> {
  const text = recoveryText.trim();
  const { pub, priv } = await decodeRecovery(text);
  await sodium.ready;
  if (sodium.to_base64(pub, sodium.base64_variants.ORIGINAL) !== plan.recovery_pubkey) throw new Error("This is not this panel's recovery key.");
  const boxes: Record<string, { agent_box: string; updated_at: number }> = {};
  for (const v of plan.vars) {
    const value = await openWith(pub, priv, v.recovery_box);
    boxes[v.id] = { agent_box: await sealTo(plan.to_pubkey, value), updated_at: v.updated_at };
    value.fill(0);
  }
  const recovery_box = plan.restore ? await sealTo(plan.to_pubkey, new TextEncoder().encode(text)) : "";
  priv.fill(0);
  return { boxes, recovery_box };
}
