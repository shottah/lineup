import { AuthGate } from "@/components/AuthGate";
import { Nav } from "@/components/Nav";
import { SettingsBody } from "./SettingsBody";

export default function SettingsPage() {
  return (
    <AuthGate>
      <Nav />
      <SettingsBody />
    </AuthGate>
  );
}
