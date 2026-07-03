export default function Home() {
  return (
    <main className="flex flex-1 flex-col items-center justify-center gap-6 px-6 text-center">
      <h1 className="text-3xl font-semibold tracking-tight text-black dark:text-zinc-50 sm:text-4xl">
        Lineup — your week of TV, planned like a lineup
      </h1>
      <a
        href="/guide"
        className="text-lg font-medium text-zinc-950 underline underline-offset-4 dark:text-zinc-50"
      >
        View your guide
      </a>
    </main>
  );
}
