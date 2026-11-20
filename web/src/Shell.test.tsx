import { describe, expect, it } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { fakeApi } from "./test-api";
import { Shell } from "./Shell";
import { Modal } from "./ui";

describe("Shell", () => {
  it("keeps Search as a button on phones: only its words hide", () => {
    fakeApi({});
    render(<Shell crumb={["Projects"]} active="/">x</Shell>);
    const b = screen.getByRole("button", { name: "Search" });
    expect(b.classList.contains("hide")).toBe(false);
    expect(b.querySelector("span.hide")?.textContent).toContain("Search");
  });

  it("ignores Cmd-K while a modal is open", () => {
    fakeApi({});
    render(<Shell crumb={["Projects"]} active="/"><Modal title="Token" onClose={() => {}}><button>ok</button></Modal></Shell>);
    fireEvent.keyDown(window, { key: "k", metaKey: true });
    expect(screen.queryByRole("dialog", { name: /search/i })).toBeNull();
    expect(screen.getAllByRole("dialog")).toHaveLength(1);
  });

  it("Cmd-K opens the palette and Cmd-K again closes it", () => {
    fakeApi({});
    render(<Shell crumb={["Projects"]} active="/">x</Shell>);
    fireEvent.keyDown(window, { key: "k", metaKey: true });
    expect(screen.getByRole("dialog", { name: "Go to" })).toBeTruthy();
    fireEvent.keyDown(window, { key: "k", metaKey: true });
    expect(screen.queryByRole("dialog", { name: "Go to" })).toBeNull();
  });

  it("Cmd-K does nothing while another dialog is open", () => {
    fakeApi({});
    render(<Shell crumb={["Projects"]} active="/"><Modal title="Other" onClose={() => {}}><button>ok</button></Modal></Shell>);
    fireEvent.keyDown(window, { key: "k", metaKey: true });
    expect(screen.queryByRole("dialog", { name: "Go to" })).toBeNull();
  });
});
