// Company and app marks: theSVG (thesvg.org, MIT; each mark belongs to its owner).
import bolt from "@thesvg/icons/bolt";
import clickhouse from "@thesvg/icons/clickhouse";
import discord from "@thesvg/icons/discord";
import docker from "@thesvg/icons/docker";
import gitea from "@thesvg/icons/gitea";
import github from "@thesvg/icons/github";
import gitlab from "@thesvg/icons/gitlab";
import grafana from "@thesvg/icons/grafana";
import hetzner from "@thesvg/icons/hetzner";
import lovable from "@thesvg/icons/lovable";
import mariadb from "@thesvg/icons/mariadb";
import mongodb from "@thesvg/icons/mongodb";
import mysql from "@thesvg/icons/mysql";
import ntfy from "@thesvg/icons/ntfy";
import postgresql from "@thesvg/icons/postgresql";
import replit from "@thesvg/icons/replit";
import slack from "@thesvg/icons/slack";
import tailscale from "@thesvg/icons/tailscale";
import telegram from "@thesvg/icons/telegram";
import v0 from "@thesvg/icons/v0";
import valkey from "@thesvg/icons/valkey";

type Mark = { svg: string; variants?: Record<string, string> };

const one = (svg: string) =>
  svg.split(/(<mask[\s\S]*?<\/mask>)/).map((part, i) => i % 2 ? part : part
    .replace(/\b(fill|stroke)="(?!none")(?!currentColor")[^"]*"/gi, '$1="currentColor"')
    .replace(/\b(fill|stroke)\s*:\s*(?!none)[^;"]+/gi, "$1:currentColor")).join("")
    .replace(/<svg(?![^>]*\sfill=)/, '<svg fill="currentColor"');

const mark = (m: Mark, variant = "mono") => one(m.variants?.[variant] ?? m.variants?.default ?? m.svg);

export const BRANDS: Record<string, string> = {
  github: mark(github), gitlab: mark(gitlab), gitea: mark(gitea), docker: mark(docker),
  postgres: mark(postgresql), mysql: mark(mysql, "light"), mongodb: mark(mongodb), mariadb: mark(mariadb),
  clickhouse: mark(clickhouse), valkey: mark(valkey),
  telegram: mark(telegram), slack: mark(slack), discord: mark(discord), ntfy: mark(ntfy),
  hetzner: mark(hetzner), tailscale: mark(tailscale), grafana: mark(grafana),
  lovable: mark(lovable), bolt: mark(bolt), v0: mark(v0), replit: mark(replit),
};
