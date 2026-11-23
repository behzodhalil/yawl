import { useEffect, useState } from "react";
import { api } from "./api";
import { navigate, useRoute } from "./router";
import { PAGES } from "./registry";
import { Claim } from "./pages/Claim";
import { Invite } from "./pages/Invite";
import { Reset } from "./pages/Reset";
import { SignIn } from "./pages/SignIn";
import { Shell } from "./Shell";
import { NotFound } from "./NotFound";
import { SessionContext, type Session } from "./session";


let afterSignIn = "";

export function App() {
  const r = useRoute();
  const [s, setS] = useState<Session | null>(null);
  useEffect(() => {
    api<Session>("GET", "/api/session").then(setS, () => setS({ claimed: true }));
  }, [r.page === "sign-in"]);
  const reload = () => void api<Session>("GET", "/api/session").then(setS, () => {});

  if (!s) return null;
  if (r.page === "invite" && location.hash.length > 1) {
    return <Invite signedIn={s.email} onDone={(email) => (setS({ claimed: true, email }), navigate("/", true))} />;
  }
  if (r.page === "reset") return <Reset onDone={() => navigate("/sign-in?reset=1", true)} />;
  if (!s.claimed) return <Claim onDone={(email) => (setS({ claimed: true, email }), navigate("/", true))} />;
  if (!s.email || r.page === "sign-in") {
    if (r.page !== "sign-in") {
      afterSignIn = location.pathname + location.hash;
      navigate("/sign-in", true);
    }
    return <SignIn onDone={(email) => (setS({ claimed: true, email }), navigate(afterSignIn || "/", true), (afterSignIn = ""))} />;
  }
  if (r.page === "claim") {
    navigate("/", true);
    return null;
  }
  const page = PAGES[r.page];
  if (!page) {
    return (
      <Shell crumb={["Not found"]} active="">
        <NotFound path={location.pathname} />
      </Shell>
    );
  }
  return <SessionContext.Provider value={{ ...s, reload }}>{page(r)}</SessionContext.Provider>;
}
