import type { Metadata } from "next";
import { PageHeader } from "@/components/page-header";
import { SendAlert } from "@/components/send-alert";

export const metadata: Metadata = { title: "Send an alert" };

export default function SendPage() {
  return (
    <div className="space-y-6">
      <PageHeader
        title="Send an alert"
        description="Preview the decision in your browser, then send the alert to the router and compare."
      />
      <SendAlert />
    </div>
  );
}
