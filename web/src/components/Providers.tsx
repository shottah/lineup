"use client";

import { createContext, useCallback, useContext, useEffect, useRef, useState } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { onAuthStateChanged, type User as FirebaseUser } from "firebase/auth";

import { auth } from "@/lib/firebase";

type AuthState = { user: FirebaseUser | null; loading: boolean };

const AuthContext = createContext<AuthState>({ user: null, loading: true });

export function useAuth(): AuthState {
  return useContext(AuthContext);
}

// --- Toast (#16): one visible message at a time, last wins, 4s
// auto-dismiss. Kept dependency-free: a single consumer today (the
// rotation_full 409) doesn't justify a package.

type ToastState = { show: (message: string) => void };

const ToastContext = createContext<ToastState>({ show: () => {} });

export function useToast(): ToastState {
  return useContext(ToastContext);
}

const TOAST_MS = 4000;

function ToastProvider({ children }: { children: React.ReactNode }) {
  const [message, setMessage] = useState<string | null>(null);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);

  const show = useCallback((next: string) => {
    setMessage(next);
    if (timer.current) {
      clearTimeout(timer.current);
    }
    timer.current = setTimeout(() => setMessage(null), TOAST_MS);
  }, []);

  useEffect(
    () => () => {
      if (timer.current) {
        clearTimeout(timer.current);
      }
    },
    [],
  );

  return (
    <ToastContext.Provider value={{ show }}>
      {children}
      {message && (
        <div
          role="status"
          className="fixed bottom-6 left-1/2 z-50 -translate-x-1/2 rounded-lg bg-zinc-950 px-4 py-2 text-sm text-zinc-50 shadow-lg dark:bg-zinc-50 dark:text-zinc-950"
        >
          {message}
        </div>
      )}
    </ToastContext.Provider>
  );
}

export function Providers({ children }: { children: React.ReactNode }) {
  // One QueryClient per component tree (not module scope): avoids sharing
  // cache state across SSR requests.
  const [queryClient] = useState(() => new QueryClient());
  const [authState, setAuthState] = useState<AuthState>({ user: null, loading: true });

  useEffect(
    () => onAuthStateChanged(auth, (user) => setAuthState({ user, loading: false })),
    [],
  );

  return (
    <QueryClientProvider client={queryClient}>
      <AuthContext.Provider value={authState}>
        <ToastProvider>{children}</ToastProvider>
      </AuthContext.Provider>
    </QueryClientProvider>
  );
}
