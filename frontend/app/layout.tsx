import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = {
  title: "COMBIO",
  description: "A memory card game. You can't see your own cards \u2014 that's the point.",
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <body className="font-display">{children}</body>
    </html>
  );
}
