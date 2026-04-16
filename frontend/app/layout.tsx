import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = {
  title: "Midas Touch",
  description: "Stock signal advisor dashboard",
};

export default function RootLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <html lang="en">
      <body>{children}</body>
    </html>
  );
}
