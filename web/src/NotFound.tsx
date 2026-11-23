import { OPEN_SEARCH } from "./Shell";
import { Icon, Link } from "./ui";

export function nearestPage(path: string): [string, string] | null {
  const [head, a, b] = path.split("/").filter(Boolean);
  if (head === "servers" && a && a !== "new") return [`/servers/${a}`, "Server overview"];
  if (head === "p" && a && b) return [`/p/${a}/${b}`, `${a} / ${b}`];
  return null;
}

export function NotFound({ path }: { path: string }) {
  const near = nearestPage(path);
  return (
    <>
      <h1>Page not found</h1>
      <p className="muted">This address does not match any page in the panel.</p>
      <div className="row">
        <Link to="/" className="btn pri">Go to Projects</Link>
        <button type="button" className="btn" onClick={() => window.dispatchEvent(new Event(OPEN_SEARCH))}><Icon name="search" />Search</button>
      </div>
      {near && <p className="muted">Did you mean <Link to={near[0]} className="l">{near[1]}</Link>?</p>}
    </>
  );
}
