import type { Metadata } from "next";
import { AlertDetail } from "@/components/alert-detail";

export const metadata: Metadata = { title: "Alert" };

export default async function AlertPage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  return <AlertDetail id={id} />;
}
