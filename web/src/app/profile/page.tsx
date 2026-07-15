import { AuthGate } from "@/components/AuthGate";
import { Nav } from "@/components/Nav";
import { ProfileBody } from "./ProfileBody";

export default function ProfilePage() {
  return (
    <AuthGate>
      <Nav />
      <ProfileBody />
    </AuthGate>
  );
}
