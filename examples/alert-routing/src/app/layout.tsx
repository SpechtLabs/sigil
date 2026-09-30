import { GeistMono } from "geist/font/mono";
import { GeistSans } from "geist/font/sans";
import type { Metadata } from "next";
import type { ReactNode } from "react";
import { EventsProvider } from "@/components/events-provider";
import { SiteHeader } from "@/components/site-header";
import { ThemeProvider } from "@/components/theme";
import { Toaster } from "@/components/ui/sonner";
import { TooltipProvider } from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";
import "./globals.css";

export const metadata: Metadata = {
  title: { default: "alertrouter", template: "%s · alertrouter" },
  description: "Alert routing decided by Sigil policies",
};

export default function RootLayout({ children }: { children: ReactNode }) {
  return (
    // next-themes sets the theme class on <html> before hydration.
    <html lang="en" className={cn(GeistSans.variable, GeistMono.variable)} suppressHydrationWarning>
      <body className="min-h-svh font-sans antialiased">
        <a
          href="#main"
          className="sr-only focus:not-sr-only focus:fixed focus:top-2 focus:left-2 focus:z-50 focus:rounded-md focus:bg-background focus:px-3 focus:py-2 focus:ring-2 focus:ring-ring"
        >
          Skip to content
        </a>
        <ThemeProvider>
          <TooltipProvider>
            <EventsProvider>
              <SiteHeader />
              <main id="main" className="mx-auto max-w-6xl px-4 py-6 md:py-8">
                {children}
              </main>
              <Toaster />
            </EventsProvider>
          </TooltipProvider>
        </ThemeProvider>
      </body>
    </html>
  );
}
