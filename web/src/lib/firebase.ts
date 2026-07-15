// Firebase app/auth singletons. When config.authEmulatorHost is set (dev),
// auth talks to the local emulator; in production the field is absent and
// this connects to live Firebase. The guard on auth.emulatorConfig keeps
// hot-module reloads from calling connectAuthEmulator twice.

import { getApps, initializeApp } from "firebase/app";
import {
  connectAuthEmulator,
  getAuth,
  GoogleAuthProvider,
  signInWithPopup,
  signOut,
} from "firebase/auth";

import { config } from "@/lib/config";

const app = getApps()[0] ?? initializeApp(config.firebase);

export const auth = getAuth(app);

if (config.authEmulatorHost && !auth.emulatorConfig) {
  connectAuthEmulator(auth, config.authEmulatorHost, { disableWarnings: true });
}

export async function signInWithGoogle(): Promise<void> {
  await signInWithPopup(auth, new GoogleAuthProvider());
}

export async function signOutUser(): Promise<void> {
  await signOut(auth);
}
