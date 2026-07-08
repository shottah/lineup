CREATE TABLE users (
  id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  firebase_uid TEXT NOT NULL UNIQUE,
  email TEXT NOT NULL,
  display_name TEXT NOT NULL DEFAULT '',
  region TEXT NOT NULL DEFAULT 'US',
  schedule_prefs JSONB NOT NULL DEFAULT '{}',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE providers (
  id BIGINT PRIMARY KEY,           -- TMDB provider id
  name TEXT NOT NULL,
  logo_path TEXT NOT NULL DEFAULT ''
);
CREATE TABLE titles (
  id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  tmdb_id BIGINT NOT NULL,
  kind TEXT NOT NULL CHECK (kind IN ('movie','series')),
  tvmaze_id BIGINT,
  name TEXT NOT NULL,
  overview TEXT NOT NULL DEFAULT '',
  poster_path TEXT NOT NULL DEFAULT '',
  runtime_minutes INT NOT NULL DEFAULT 0,  -- movie runtime / typical ep runtime
  airing BOOLEAN NOT NULL DEFAULT false,
  refreshed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  providers_refreshed_at TIMESTAMPTZ NOT NULL DEFAULT 'epoch',
  airings_refreshed_at TIMESTAMPTZ NOT NULL DEFAULT 'epoch',
  UNIQUE (kind, tmdb_id)
);
CREATE TABLE title_seasons (
  title_id BIGINT NOT NULL REFERENCES titles(id) ON DELETE CASCADE,
  season_number INT NOT NULL,
  episode_count INT NOT NULL,
  PRIMARY KEY (title_id, season_number)
);
CREATE TABLE title_airings (
  title_id BIGINT NOT NULL REFERENCES titles(id) ON DELETE CASCADE,
  season INT NOT NULL, episode INT NOT NULL,
  air_date DATE NOT NULL,
  PRIMARY KEY (title_id, season, episode)
);
CREATE TABLE title_providers (
  title_id BIGINT NOT NULL REFERENCES titles(id) ON DELETE CASCADE,
  region TEXT NOT NULL,
  provider_id BIGINT NOT NULL REFERENCES providers(id),
  PRIMARY KEY (title_id, region, provider_id)
);
CREATE TABLE user_titles (
  user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  title_id BIGINT NOT NULL REFERENCES titles(id) ON DELETE CASCADE,
  status TEXT NOT NULL DEFAULT 'none' CHECK (status IN ('none','watchlist','rotation','watched')),
  rating NUMERIC(2,1) CHECK (rating >= 0.5 AND rating <= 5.0),
  favorite BOOLEAN NOT NULL DEFAULT false,
  pointer_season INT NOT NULL DEFAULT 1,
  pointer_episode INT NOT NULL DEFAULT 1,
  added_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  watched_at TIMESTAMPTZ,
  PRIMARY KEY (user_id, title_id)
);
CREATE TABLE guides (
  id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  start_date DATE NOT NULL,
  end_date DATE NOT NULL,
  seed BIGINT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX guides_user_created ON guides(user_id, created_at DESC);
CREATE TABLE guide_items (
  id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  guide_id BIGINT NOT NULL REFERENCES guides(id) ON DELETE CASCADE,
  date DATE NOT NULL,
  start_min INT NOT NULL,  -- minutes from midnight local
  end_min INT NOT NULL,
  title_id BIGINT NOT NULL REFERENCES titles(id),
  season INT NOT NULL DEFAULT 0,  -- 0 = movie
  episode INT NOT NULL DEFAULT 0,
  provider_id BIGINT NOT NULL REFERENCES providers(id),
  is_plan BOOLEAN NOT NULL DEFAULT true,
  pinned BOOLEAN NOT NULL DEFAULT false,
  edited BOOLEAN NOT NULL DEFAULT false,
  watched BOOLEAN NOT NULL DEFAULT false
);
CREATE INDEX guide_items_guide_date ON guide_items(guide_id, date, start_min);
