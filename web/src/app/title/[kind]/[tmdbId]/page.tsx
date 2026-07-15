import { AuthGate } from "@/components/AuthGate";
import { Nav } from "@/components/Nav";
import { TitleBody } from "./TitleBody";

// Next 16: params is a Promise in server components.
export default async function TitlePage({
  params,
}: {
  params: Promise<{ kind: string; tmdbId: string }>;
}) {
  const { kind, tmdbId } = await params;
  return (
    <AuthGate>
      <Nav />
      <TitleBody kind={kind} tmdbId={tmdbId} />
    </AuthGate>
  );
}
