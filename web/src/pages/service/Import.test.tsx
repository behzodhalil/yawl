import { afterEach, describe, expect, it, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { fakeApi } from "../../test-api";
import { ImportDump } from "./Import";

const D = "/api/services/acme/production/acme-db";

class FakeXHR {
  static last: FakeXHR;
  upload: { onprogress: ((e: { lengthComputable: boolean; loaded: number; total: number }) => void) | null } = { onprogress: null };
  onload: (() => void) | null = null;
  onerror: (() => void) | null = null;
  status = 0;
  responseText = "";
  method = "";
  url = "";
  headers: Record<string, string> = {};
  body: unknown;
  constructor() { FakeXHR.last = this; }
  open(m: string, u: string) { this.method = m; this.url = u; }
  setRequestHeader(k: string, v: string) { this.headers[k] = v; }
  send(b: unknown) { this.body = b; }
  answer(status: number, text: string) { this.status = status; this.responseText = text; this.onload?.(); }
}
afterEach(() => vi.unstubAllGlobals());

const confirmWith = (button: string) => {
  fireEvent.click(screen.getByRole("button", { name: button }));
  const yes = screen.getByRole("button", { name: "Yes, import" }) as HTMLButtonElement;
  expect(screen.getByRole("dialog", { name: "Import into acme-db?" })).toBeTruthy();
  expect(screen.getByText("A snapshot of acme-db is kept first. What the dump drops or replaces is changed here.")).toBeTruthy();
  expect(yes.disabled).toBe(true);
  fireEvent.change(screen.getByLabelText("Type acme-db to confirm"), { target: { value: "acme-db" } });
  expect(yes.disabled).toBe(false);
  fireEvent.click(yes);
};

describe("ImportDump", () => {
  it("imports from a link and from the server's folder, after a typed confirm", async () => {
    const calls = fakeApi({ [`POST ${D}/import`]: { deploy: "dep_9" } });
    render(<ImportDump project="acme" env="production" service="acme-db" />);
    fireEvent.change(screen.getByLabelText("Link"), { target: { value: "https://bucket.example/d.sql.gz" } });
    fireEvent.click(screen.getByRole("button", { name: "Import from the link" }));
    expect(calls.some((c) => c.method === "POST")).toBe(false);
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(calls.some((c) => c.method === "POST")).toBe(false);
    confirmWith("Import from the link");
    await screen.findByText("Follow the import");
    expect(calls.find((c) => c.method === "POST")!.body).toEqual({ url: "https://bucket.example/d.sql.gz" });
    fireEvent.change(screen.getByLabelText("File on the server"), { target: { value: "old.sql" } });
    confirmWith("Import from the server");
    await vi.waitFor(() => expect(calls.filter((c) => c.method === "POST")[1].body).toEqual({ path: "old.sql" }));
  });

  it("uploads a file as the request body, named, and shows its progress", async () => {
    vi.stubGlobal("XMLHttpRequest", FakeXHR);
    render(<ImportDump project="acme" env="production" service="acme-db" />);
    const file = new File(["CREATE TABLE t ();"], "heroku.sql");
    fireEvent.change(screen.getByLabelText("Dump file"), { target: { files: [file] } });
    confirmWith("Upload and import");
    const x = FakeXHR.last;
    expect(x.url).toBe(`${D}/import?name=heroku.sql`);
    expect(x.method).toBe("PUT");
    expect(x.body).toBe(file);
    expect(x.headers["X-Yawl-Request"]).toBe("1");
    x.upload.onprogress!({ lengthComputable: true, loaded: 42, total: 100 });
    expect(await screen.findByText("Uploading 42%")).toBeTruthy();
    x.answer(200, JSON.stringify({ deploy: "dep_8" }));
    await screen.findByText("Follow the import");
  });

  it("shows the server's words when the upload is refused", async () => {
    vi.stubGlobal("XMLHttpRequest", FakeXHR);
    render(<ImportDump project="acme" env="production" service="acme-db" />);
    fireEvent.change(screen.getByLabelText("Dump file"), { target: { files: [new File(["x"], "a.sql")] } });
    confirmWith("Upload and import");
    FakeXHR.last.answer(413, "too big\n");
    expect(await screen.findByText("too big")).toBeTruthy();
  });
});
