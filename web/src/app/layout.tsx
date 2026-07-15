import type { Metadata } from "next";
import { Albert_Sans, Geist_Mono } from "next/font/google";
import "./globals.css";

import { Providers } from "@/components/Providers";

const albertSans = Albert_Sans({
  variable: "--font-albert-sans",
  subsets: ["latin"],
  weight: ["400", "500", "600", "700"],
});

const geistMono = Geist_Mono({
  variable: "--font-geist-mono",
  subsets: ["latin"],
});

export const metadata: Metadata = {
  title: "Lineup",
  description: "Your week of TV, planned like a lineup",
};

// Runs before paint so the first frame already has the right theme: reads
// the persisted choice (falling back to dark) and stamps it on <html>
// before React hydrates, avoiding a light/dark flash.
const THEME_INIT_SCRIPT =
  'try{document.documentElement.dataset.lt=localStorage.getItem("lineup-theme")||"dark"}catch(e){document.documentElement.dataset.lt="dark"}';

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  return (
    <html
      lang="en"
      className={`${albertSans.variable} ${geistMono.variable} h-full antialiased`}
      suppressHydrationWarning
    >
      <head>
        <script dangerouslySetInnerHTML={{ __html: THEME_INIT_SCRIPT }} />
      </head>
      <body className="min-h-full flex flex-col">
        <Providers>{children}</Providers>
      </body>
    </html>
  );
}
