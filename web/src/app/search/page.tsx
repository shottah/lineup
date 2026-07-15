import { AuthGate } from "@/components/AuthGate";
import { Nav } from "@/components/Nav";
import { SearchBody } from "./SearchBody";

export default function SearchPage() {
  return (
    <AuthGate>
      <Nav />
      <SearchBody />
    </AuthGate>
  );
}
