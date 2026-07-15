"use client";

import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import { useEffect, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";

import { useAuth } from "@/components/Providers";
import { ThemeToggle } from "@/components/ThemeToggle";
import { signOutUser } from "@/lib/firebase";

const NAV_ITEMS = [
  { href: "/guide", label: "Guide" },
  { href: "/search", label: "Search" },
  { href: "/profile", label: "Profile" },
  { href: "/settings", label: "Settings" },
] as const;

export function Nav() {
  const { user } = useAuth();
  const pathname = usePathname();
  const router = useRouter();
  const queryClient = useQueryClient();
  const [menuOpen, setMenuOpen] = useState(false);
  const menuRef = useRef<HTMLDivElement>(null);
  const avatarButtonRef = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    if (!menuOpen) return;

    function onPointerDown(event: MouseEvent) {
      if (menuRef.current && !menuRef.current.contains(event.target as Node)) {
        setMenuOpen(false);
      }
    }
    function onKeyDown(event: KeyboardEvent) {
      if (event.key === "Escape") {
        setMenuOpen(false);
        // Return focus to the trigger so keyboard users don't lose their
        // place when Escape closes the menu.
        avatarButtonRef.current?.focus();
      }
    }

    document.addEventListener("mousedown", onPointerDown);
    document.addEventListener("keydown", onKeyDown);
    return () => {
      document.removeEventListener("mousedown", onPointerDown);
      document.removeEventListener("keydown", onKeyDown);
    };
  }, [menuOpen]);

  const initial = user?.email ? user.email[0].toUpperCase() : "?";

  return (
    <header className="relative flex items-center justify-between gap-4 border-b border-line px-8 py-[18px]">
      <div className="flex items-center gap-7">
        <Link href="/guide" className="text-[19px] font-semibold tracking-[-0.01em] text-ink">
          Lineup
        </Link>
        <nav className="flex gap-1">
          {NAV_ITEMS.map((item) => {
            const active = pathname === item.href;
            return (
              <Link
                key={item.href}
                href={item.href}
                className={`rounded-full px-[15px] py-[7px] text-[13px] font-medium ${
                  active ? "bg-acc-soft text-acc" : "text-mut"
                }`}
              >
                {item.label}
              </Link>
            );
          })}
        </nav>
      </div>
      <div className="flex items-center gap-2.5">
        <ThemeToggle />
        <div ref={menuRef}>
          <button
            ref={avatarButtonRef}
            type="button"
            onClick={() => setMenuOpen((open) => !open)}
            aria-label="Account"
            aria-haspopup="menu"
            aria-expanded={menuOpen}
            className="flex h-8 w-8 items-center justify-center rounded-full border border-line bg-acc-soft text-[13px] font-semibold text-acc"
          >
            {initial}
          </button>
          {menuOpen && (
            <div className="absolute right-8 top-14 z-50 min-w-[200px] rounded-xl border border-line bg-panel p-2 shadow-lg">
              <p className="truncate px-3 py-2 text-xs text-mut">{user?.email}</p>
              <button
                type="button"
                onClick={async () => {
                  setMenuOpen(false);
                  await signOutUser();
                  // Drop all cached query data so the next account never sees the
                  // previous account's profile served from cache.
                  queryClient.clear();
                  router.replace("/login");
                }}
                className="w-full rounded-lg px-3 py-2 text-left text-[13px] text-ink hover:bg-panel2"
              >
                Sign out
              </button>
            </div>
          )}
        </div>
      </div>
    </header>
  );
}
