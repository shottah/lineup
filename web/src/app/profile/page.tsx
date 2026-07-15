import { AuthGate } from "@/components/AuthGate";
import { Footer } from "@/components/Footer";
import { Nav } from "@/components/Nav";
import { ProfileBody } from "./ProfileBody";

export default function ProfilePage() {
  return (
    <AuthGate>
      <Nav />
      <ProfileBody />
      <Footer />
    </AuthGate>
  );
}
