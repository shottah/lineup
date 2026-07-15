import { AuthGate } from "@/components/AuthGate";
import { Footer } from "@/components/Footer";
import { Nav } from "@/components/Nav";

import { GuideBody } from "./GuideBody";

export default function GuidePage() {
  return (
    <AuthGate>
      <Nav />
      <GuideBody />
      <Footer />
    </AuthGate>
  );
}
