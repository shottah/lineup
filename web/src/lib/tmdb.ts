// TMDB image URL assembly. poster_path/logo_path come from the API
// verbatim (e.g. "/abc.jpg", or "" when TMDB has none).

const IMAGE_BASE = "https://image.tmdb.org/t/p";

export type PosterSize = "w92" | "w342";

export function posterUrl(path: string, size: PosterSize): string | null {
  if (!path) {
    return null;
  }
  return `${IMAGE_BASE}/${size}${path}`;
}
