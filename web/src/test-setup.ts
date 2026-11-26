import { afterEach, vi } from "vitest";
import { cleanup } from "@testing-library/react";
import { drawn } from "./test-echarts";

vi.mock("./echarts", () => import("./test-echarts"));

const topLayer: HTMLElement[] = [];

afterEach(() => {
  cleanup();
  drawn.length = 0;
  topLayer.length = 0;
});

const popoverOpen = "data-popover-open";
const popoverStyle = document.createElement("style");
popoverStyle.textContent = `[popover][${popoverOpen}]{display:block}`;
document.head.append(popoverStyle);

function leaveTopLayer(el: HTMLElement) {
  const at = topLayer.indexOf(el);
  if (at >= 0) topLayer.splice(at, 1);
}

HTMLDialogElement.prototype.showModal = function () {
  this.open = true;
  topLayer.push(this);
};

HTMLDialogElement.prototype.close = function () {
  if (!this.open) return;
  this.open = false;
  leaveTopLayer(this);
  this.dispatchEvent(new Event("close"));
};

HTMLElement.prototype.showPopover = function () {
  this.setAttribute(popoverOpen, "");
  topLayer.push(this);
};

HTMLElement.prototype.hidePopover = function () {
  if (!this.hasAttribute(popoverOpen)) return;
  this.removeAttribute(popoverOpen);
  leaveTopLayer(this);
  this.dispatchEvent(Object.assign(new Event("toggle"), { oldState: "open", newState: "closed" }));
};

window.addEventListener("keydown", (e) => {
  if (e.key !== "Escape" || e.defaultPrevented) return;
  const top = topLayer.filter((el) => el.isConnected).at(-1);
  if (top instanceof HTMLDialogElement) {
    if (top.dispatchEvent(new Event("cancel", { cancelable: true }))) top.close();
  } else top?.hidePopover();
});

window.IntersectionObserver ??= class {
  observe() {}
  unobserve() {}
  disconnect() {}
  takeRecords() { return []; }
} as unknown as typeof IntersectionObserver;
Object.defineProperty(window, "matchMedia", { writable: true, value: (query: string) => ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false }) });

window.ResizeObserver ??= class {
  observe() {}
  unobserve() {}
  disconnect() {}
} as unknown as typeof ResizeObserver;

Element.prototype.scrollIntoView ??= function () {};

Element.prototype.getAnimations ??= () => [];
