import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = {
  title: "Record Hub",
  description: "Workspace records, schemas, and projection operations",
};

export default function RootLayout({ children }: Readonly<{ children: React.ReactNode }>) {
  return (
    <html lang="zh-CN">
      <body>{children}</body>
    </html>
  );
}
