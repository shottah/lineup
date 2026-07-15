"use client";

import { createContext, useContext, useEffect, useState } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { onAuthStateChanged, type User as FirebaseUser } from "firebase/auth";

import { auth } from "@/lib/firebase";

type AuthState = { user: FirebaseUser | null; loading: boolean };

const AuthContext = createContext<AuthState>({ user: null, loading: true });

export function useAuth(): AuthState {
  return useContext(AuthContext);
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
      <AuthContext.Provider value={authState}>{children}</AuthContext.Provider>
    </QueryClientProvider>
  );
}
