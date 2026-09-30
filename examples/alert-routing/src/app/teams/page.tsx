import type { Metadata } from "next";
import { PageHeader } from "@/components/page-header";
import { TeamsTable } from "@/components/teams-table";

export const metadata: Metadata = { title: "Teams" };

export default function TeamsPage() {
  return (
    <div className="space-y-6">
      <PageHeader
        title="Teams"
        description="The team directory: an alert's team label picks the entry, and <team>.alerts decides with it."
      />
      <TeamsTable />
    </div>
  );
}
