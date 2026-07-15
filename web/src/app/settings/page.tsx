import { AuthGate } from "@/components/AuthGate";
import { Footer } from "@/components/Footer";
import { Nav } from "@/components/Nav";
import { SettingsBody } from "./SettingsBody";

export default function SettingsPage() {
  return (
    <AuthGate>
      <Nav />
      <SettingsBody />
      <Footer />
    </AuthGate>
  );
}
