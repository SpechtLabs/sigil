import type { Metadata } from "next";
import { NotificationsInbox } from "@/components/notifications-inbox";
import { PageHeader } from "@/components/page-header";

export const metadata: Metadata = { title: "Notifications" };

export default function NotificationsPage() {
  return (
    <div className="space-y-6">
      <PageHeader
        title="Notifications"
        description="What the dispatcher sent, grouped by where it went: on-call pages, channel posts and drops."
      />
      <NotificationsInbox />
    </div>
  );
}
