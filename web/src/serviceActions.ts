import { api } from "./api";

export const startDeploy = async (path: string, noCache = false) =>
  (await api<{ deploy_id: string }>("POST", path + "/deploys", noCache ? { commit: "", no_cache: true } : { commit: "" })).deploy_id;

export const restartService = (path: string) => api("POST", path + "/restart");
