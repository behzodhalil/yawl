import { describe, expect, it } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { fakeApi } from "../../test-api";
import { Storage } from "./Storage";

describe("Storage copies", () => {
  it("lists copies, adds one and removes one", async () => {
    const calls = fakeApi({
      "GET /api/settings/storage": { set: true, endpoint: "s3.example", bucket: "b", access_key_end: "1234",
        copies: [{ name: "eu", endpoint: "eu.s3.example", bucket: "eu-copies", access_key_end: "9876" }] },
      "PUT /api/settings/storage/copies/us": null,
      "DELETE /api/settings/storage/copies/eu": null,
    });
    render(<Storage />);
    await screen.findByText("eu-copies");
    fireEvent.change(screen.getByLabelText("Copy name"), { target: { value: "us" } });
    fireEvent.change(screen.getByLabelText("Copy endpoint"), { target: { value: "us.s3.example" } });
    fireEvent.change(screen.getByLabelText("Copy bucket"), { target: { value: "us-copies" } });
    fireEvent.change(screen.getByLabelText("Copy access key"), { target: { value: "AK" } });
    fireEvent.change(screen.getByLabelText("Copy secret key"), { target: { value: "SK" } });
    fireEvent.click(screen.getByRole("button", { name: "Add copy" }));
    await waitFor(() => expect(calls.some((c) => c.method === "PUT" && c.url.endsWith("/copies/us"))).toBe(true));
    expect(calls.find((c) => c.method === "PUT")!.body).toMatchObject({ endpoint: "us.s3.example", bucket: "us-copies", access_key: "AK", secret_key: "SK" });
    fireEvent.click(screen.getByRole("button", { name: "Remove eu" }));
    fireEvent.click(await screen.findByRole("button", { name: "Yes, remove" }));
    await waitFor(() => expect(calls.some((c) => c.method === "DELETE")).toBe(true));
  });

  it("asks before it removes a copy", async () => {
    const calls = fakeApi({ "GET /api/settings/storage": { set: true, endpoint: "s3.example", bucket: "main", access_key_end: "1234", copies: [{ name: "eu", endpoint: "eu.s3.example", bucket: "eu-copies", access_key_end: "9876" }] }, "DELETE /api/settings/storage/copies/eu": null });
    render(<Storage />);
    const sent = () => calls.filter((c) => c.method !== "GET");
    fireEvent.click(await screen.findByRole("button", { name: "Remove eu" }));
    expect(await screen.findByText('Remove the copy "eu"?')).toBeTruthy();
    expect(sent()).toHaveLength(0);
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(sent()).toHaveLength(0);
    fireEvent.click(await screen.findByRole("button", { name: "Remove eu" }));
    fireEvent.click(await screen.findByRole("button", { name: "Yes, remove" }));
    await waitFor(() => expect(sent()).toEqual([{ method: "DELETE", url: "/api/settings/storage/copies/eu", body: undefined }]));
  });
});
