"use client";

import { RouteIcon } from "lucide-react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { useEvents } from "@/components/events-provider";
import { ThemeToggle } from "@/components/theme";
import { cn } from "@/lib/utils";

const NAV = [
  { href: "/", label: "Overview" },
  { href: "/send", label: "Send an alert" },
  { href: "/notifications", label: "Notifications" },
  { href: "/policies", label: "Policies" },
  { href: "/teams", label: "Teams" },
] as const;

function active(pathname: string, href: string): boolean {
  if (href === "/") return pathname === "/" || pathname.startsWith("/alerts/");
  return pathname === href || pathname.startsWith(`${href}/`);
}

export function SiteHeader() {
  const pathname = usePathname();
  return (
    <header className="sticky top-0 z-40 border-b bg-background/95 backdrop-blur supports-backdrop-filter:bg-background/80">
      <div className="mx-auto flex h-14 max-w-6xl items-center gap-4 px-4">
        <Link href="/" className="flex items-center gap-2 font-semibold">
          <RouteIcon className="size-5" aria-hidden />
          <span>alertrouter</span>
        </Link>
        <nav aria-label="Main" className="-mx-1 flex min-w-0 flex-1 items-center gap-1 overflow-x-auto">
          {NAV.map((item) => {
            const current = active(pathname, item.href);
            return (
              <Link
                key={item.href}
                href={item.href}
                aria-current={current ? "page" : undefined}
                className={cn(
                  "rounded-md px-2.5 py-1.5 text-sm whitespace-nowrap text-muted-foreground transition-colors hover:text-foreground focus-visible:ring-[3px] focus-visible:ring-ring/50 focus-visible:outline-none",
                  current && "bg-muted text-foreground",
                )}
              >
                {item.label}
              </Link>
            );
          })}
        </nav>
        <StreamIndicator />
        <ThemeToggle />
      </div>
    </header>
  );
}

/** Whether the live feed is connected, as a dot and a label screen readers announce on change. */
function StreamIndicator() {
  const { stream } = useEvents();
  const label = { connecting: "Connecting", live: "Live", reconnecting: "Reconnecting" }[stream];
  return (
    <output aria-live="polite" className="hidden items-center gap-1.5 text-xs text-muted-foreground sm:flex">
      <span
        aria-hidden
        className={cn(
          "size-2 rounded-full",
          stream === "live" ? "bg-emerald-500" : stream === "connecting" ? "bg-muted-foreground" : "bg-amber-500",
        )}
      />
      {label}
    </output>
  );
}
