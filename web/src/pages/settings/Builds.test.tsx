import { describe, expect, it } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { chooseOption, fakeApi } from "../../test-api";
import { Builds } from "./Builds";

describe("Builds", () => {
  it("picks a build server and the repository it pushes to, and puts builds back", async () => {
    const calls = fakeApi({
      "GET /api/settings/builds": { server: "", repository: "" },
      "GET /api/servers": [{ name: "hel1" }, { name: "builder" }],
      "PUT /api/settings/builds": null,
    });
    render(<Builds />);
    await screen.findByRole("combobox", { name: "Build on" });
    await chooseOption("Build on", "builder");
    fireEvent.change(screen.getByLabelText("Push to"), { target: { value: "ghcr.io/acme/builds" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(calls.find((c) => c.method === "PUT")?.body).toEqual({ server: "builder", repository: "ghcr.io/acme/builds" }));
    await chooseOption("Build on", "Each server builds its own");
    await waitFor(() => expect(screen.queryByLabelText("Push to")).toBeNull());
  });
});
