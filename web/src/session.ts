import { createContext } from "react";

export type Session = { claimed: boolean; email?: string; adopting?: boolean; managed?: boolean; reload?: () => void };

export const SessionContext = createContext<Session>({ claimed: true });
