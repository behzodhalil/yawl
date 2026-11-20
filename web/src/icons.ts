// Interface icons: Hugeicons, the free stroke-rounded set (hugeicons.com, MIT).
import {
  Activity01Icon, Add01Icon, ArrowDown01Icon, ArrowRight01Icon, ArrowUp01Icon, Cancel01Icon, Clock01Icon, Copy01Icon,
  DashboardSquare01Icon, Database01Icon, Folder01Icon, Globe02Icon, Key01Icon, Layers01Icon, Mail01Icon, Moon02Icon,
  MoreHorizontalIcon, Notification01Icon, PackageIcon, Search01Icon, SentIcon, ServerStack01Icon, Settings01Icon,
  SourceCodeIcon, Sun03Icon, Tick02Icon, UserGroupIcon, WebhookIcon, WorkflowSquare03Icon, Wrench01Icon,
} from "@hugeicons/core-free-icons";

export const ICONS: Record<string, typeof Add01Icon> = {
  users: UserGroupIcon, server: ServerStack01Icon, bell: Notification01Icon, log: Activity01Icon, layers: Layers01Icon,
  key: Key01Icon, code: SourceCodeIcon, folder: Folder01Icon, check: Tick02Icon, cross: Cancel01Icon, x: Cancel01Icon,
  chev: ArrowRight01Icon, plus: Add01Icon, search: Search01Icon, globe: Globe02Icon, db: Database01Icon, cog: Settings01Icon,
  worker: WorkflowSquare03Icon, copy: Copy01Icon, send: SentIcon, theme: Moon02Icon, sun: Sun03Icon, down: ArrowDown01Icon,
  up: ArrowUp01Icon, more: MoreHorizontalIcon, box: PackageIcon, cron: Clock01Icon, template: DashboardSquare01Icon,
  build: Wrench01Icon, mail: Mail01Icon, webhook: WebhookIcon,
};

import { Alert02Icon, FlashIcon, FullScreenIcon, InformationCircleIcon, ArrowUp02Icon } from "@hugeicons/core-free-icons";
Object.assign(ICONS, { "log-info": InformationCircleIcon, "log-alert": Alert02Icon, flash: FlashIcon, "to-top": ArrowUp02Icon, fullscreen: FullScreenIcon });

import { Cancel01Icon as StatusBad, Loading03Icon, MinusSignIcon, Tick02Icon as StatusOk } from "@hugeicons/core-free-icons";
Object.assign(ICONS, { "status-ok": StatusOk, "status-busy": Loading03Icon, "status-bad": StatusBad, "status-off": MinusSignIcon });

import { Pulse01Icon } from "@hugeicons/core-free-icons";
Object.assign(ICONS, { pulse: Pulse01Icon });

import { UserIcon } from "@hugeicons/core-free-icons";
Object.assign(ICONS, { person: UserIcon });
