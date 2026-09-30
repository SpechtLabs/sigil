import { BellRingIcon, MessageSquareIcon, VolumeXIcon } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";

/** page, drop or notify, each with its own icon so the difference isn't carried by color alone. */
export function DecisionBadge({ decision, className }: { decision: string; className?: string }) {
  switch (decision) {
    case "page":
      return (
        <Badge variant="destructive" className={className}>
          <BellRingIcon aria-hidden /> page
        </Badge>
      );
    case "drop":
      return (
        <Badge variant="outline" className={cn("text-muted-foreground", className)}>
          <VolumeXIcon aria-hidden /> drop
        </Badge>
      );
    case "notify":
      return (
        <Badge variant="secondary" className={className}>
          <MessageSquareIcon aria-hidden /> notify
        </Badge>
      );
    default:
      return (
        <Badge variant="outline" className={className}>
          {decision || "none"}
        </Badge>
      );
  }
}

const SEVERITY_DOT: Record<string, string> = {
  critical: "bg-red-500",
  warning: "bg-amber-500",
  info: "bg-sky-500",
};

export function SeverityBadge({ severity }: { severity: string }) {
  return (
    <span className="inline-flex items-center gap-1.5 text-sm">
      <span aria-hidden className={cn("size-2 rounded-full", SEVERITY_DOT[severity] ?? "bg-muted-foreground")} />
      {severity || "none"}
    </span>
  );
}

/**
 * How the router handled the alert. Only routed means the team's policy
 * decided; the others went to the kind's default or a fallback.
 */
export function StatusBadge({ status }: { status: string }) {
  if (status === "routed") return null;
  return (
    <Badge variant={status.endsWith("failed") ? "destructive" : "outline"} className="font-normal">
      {status.replace("_", " ")}
    </Badge>
  );
}
