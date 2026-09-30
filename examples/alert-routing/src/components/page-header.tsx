import type { ReactNode } from "react";

export function PageHeader({
  title,
  description,
  actions,
}: {
  title: string;
  description?: string;
  actions?: ReactNode;
}) {
  return (
    <div className="flex flex-col gap-3 sm:flex-row sm:items-end sm:justify-between">
      <div className="space-y-1">
        <h1 className="text-2xl font-semibold tracking-tight">{title}</h1>
        {description !== undefined && <p className="text-muted-foreground">{description}</p>}
      </div>
      {actions !== undefined && <div className="flex gap-2">{actions}</div>}
    </div>
  );
}
