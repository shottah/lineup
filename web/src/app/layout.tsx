import type { Metadata } from "next";
import { Albert_Sans } from "next/font/google";
import "./globals.css";

import { Providers } from "@/components/Providers";

const albertSans = Albert_Sans({
  variable: "--font-albert-sans",
  subsets: ["latin"],
  weight: ["400", "500", "600", "700"],
});

export const metadata: Metadata = {
  title: "Lineup",
  description: "Your week of TV, planned like a lineup",
};

// Runs before paint so the first frame already has the right theme: reads
// the persisted choice (falling back to dark) and stamps it on <html>
// before React hydrates, avoiding a light/dark flash. Sanitized: only the
// literal "light" is honored — anything else (missing key, corrupted
// value, a future theme name) resolves to "dark".
const THEME_INIT_SCRIPT =
  'try{var v=localStorage.getItem("lineup-theme");document.documentElement.dataset.lt=v==="light"?"light":"dark"}catch(e){document.documentElement.dataset.lt="dark"}';

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  return (
    <html
      lang="en"
      data-lt="dark"
      className={`${albertSans.variable} h-full antialiased`}
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
