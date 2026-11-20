import type { ServiceView } from "../../types";

export const apiView: ServiceView = {
  id: "svc_1",
  form: { name: "api", kind: "web", repo: "https://github.com/acme/api", branch: "main", port: 10000, health_path: "/health", health_timeout: 60,
    drain_timeout: 45, auto_deploy: false, release: ["/app", "migrate"], release_timeout: 180, domains: ["api.acme.uz"], version: 0, backup_at: "", outside_port: 0, allow: [],
    schedule: "", timezone: "", command: [], timeout: 0, volumes: [], cpu: 0, memory: "", image: "", backup: false },
  url: "https://api-production.1-2-3-4.sslip.io", health: "ok", server: "hel1", current_deploy_id: "dep_2",
  domains: [{ host: "api.acme.uz", dns_state: "wrong", dns_detail: "api.acme.uz points to 9.9.9.9. Set an A record: api.acme.uz → 1.2.3.4.", cert_state: "pending" }],
};
export const S = "/api/services/acme/production/api";
