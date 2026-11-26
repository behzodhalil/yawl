import { Button, linkVariants } from "@cloudflare/kumo";
import { OPEN_SEARCH } from "./Shell";
import { ButtonLink, Icon, Link } from "./ui";

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
      <p className="text-kumo-subtle">This address does not match any page in the panel.</p>
      <div className="flex flex-wrap items-center gap-2.5">
        <ButtonLink to="/" variant="primary">Go to Projects</ButtonLink>
        <Button icon={<Icon name="search" />} onClick={() => window.dispatchEvent(new Event(OPEN_SEARCH))}>Search</Button>
      </div>
      {near && <p className="text-kumo-subtle">Did you mean <Link to={near[0]} className={linkVariants({ variant: "inline" })}>{near[1]}</Link>?</p>}
    </>
  );
}
