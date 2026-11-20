import type { ReactElement } from "react";
import type { Route } from "./router";

export const PAGES: Record<string, (r: Route) => ReactElement> = {};
