import { afterEach, describe, expect, it, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { fakeApi } from "../../test-api";
import { GitHub } from "./GitHub";

afterEach(() => vi.unstubAllGlobals());

describe("GitHub", () => {
  it("says it is connected and offers the install link, with no connect button", async () => {
    fakeApi({
      "GET /api/github": { connected: true, slug: "yawl-ab12", install_url: "https://github.com/apps/yawl-ab12/installations/new" },
      "GET /api/settings/commit-status": { github: "" },
    });
    render(<GitHub />);
    expect(await screen.findByText("Connected as yawl-ab12")).toBeTruthy();
    expect(screen.getByRole("link", { name: /github\.com\/apps\/yawl-ab12\/installations\/new/ }).getAttribute("href")).toBe("https://github.com/apps/yawl-ab12/installations/new");
    expect(screen.queryByRole("button", { name: /GitHub$/ })).toBeNull();
  });

  it("offers a link when the pop-up is blocked", async () => {
    fakeApi({
      "GET /api/github": { connected: false },
      "GET /api/settings/commit-status": { github: "" },
      "POST /api/github/connect": { url: "https://github.com/settings/apps/new?state=x" },
    });
    vi.stubGlobal("open", () => null);
    render(<GitHub />);
    fireEvent.click(await screen.findByRole("button", { name: "Connect GitHub" }));
    const a = await screen.findByRole("link", { name: "Open GitHub to finish" });
    expect(a.getAttribute("href")).toBe("https://github.com/settings/apps/new?state=x");
  });
});
