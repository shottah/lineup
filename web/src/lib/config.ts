// Client configuration, switched on NODE_ENV at build time.
// Every value here ships to the browser and is public by design; the
// Firebase apiKey is a client identifier (abuse-limited via API key
// restrictions in GCP), NOT a secret. Real secrets live in Secret Manager
// and never in this file.

type AppConfig = {
  firebase: { apiKey: string; authDomain: string; projectId: string };
  apiUrl: string;
  /** Auth emulator origin. Present only in development; its absence keeps
   *  emulator wiring out of production bundles. */
  authEmulatorHost?: string;
  appleAuth: boolean;
};

const configs: Record<"development" | "production", AppConfig> = {
  development: {
    firebase: {
      apiKey: "FILLED_IN_BY_RUNBOOK_STEP_3",
      authDomain: "lineup-app-ae6b.firebaseapp.com",
      projectId: "demo-lineup",
    },
    apiUrl: "http://localhost:8080",
    authEmulatorHost: "http://localhost:9099",
    appleAuth: false,
  },
  production: {
    firebase: {
      apiKey: "FILLED_IN_BY_RUNBOOK_STEP_3",
      authDomain: "lineup-app-ae6b.firebaseapp.com",
      projectId: "lineup-app-ae6b",
    },
    apiUrl: "https://lineup-api-zzwkjc5sdq-uc.a.run.app",
    appleAuth: false,
  },
};

export const config =
  configs[process.env.NODE_ENV === "production" ? "production" : "development"];
