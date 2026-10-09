-- A service gets its row with its first rollout.
CREATE TABLE services (
  name       TEXT    PRIMARY KEY,
  stable     INTEGER NOT NULL DEFAULT 0,   -- the stable generation; 0: none
  created_at INTEGER NOT NULL              -- unix nanoseconds
);

-- A generation is what a service runs. It is numbered from 1 within its
-- service and never changed.
CREATE TABLE generations (
  service    TEXT    NOT NULL REFERENCES services(name),
  number     INTEGER NOT NULL,
  binary     TEXT    NOT NULL,   -- an absolute path under /nix/store
  args       TEXT    NOT NULL,   -- a JSON array of strings
  closure    TEXT    NOT NULL,   -- a JSON array of names of store paths
  created_at INTEGER NOT NULL,   -- unix nanoseconds
  created_by TEXT    NOT NULL,   -- endpoint ID
  PRIMARY KEY (service, number)
);

-- A rollout is the move of a service to a generation.
CREATE TABLE rollouts (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  service     TEXT    NOT NULL,
  generation  INTEGER NOT NULL,
  parallel    INTEGER NOT NULL,              -- 0: all at once
  state       TEXT    NOT NULL,              -- 'preparing', 'activating', 'completed', 'halted' or 'superseded'
  reason      TEXT    NOT NULL DEFAULT '',
  created_at  INTEGER NOT NULL,              -- unix nanoseconds
  created_by  TEXT    NOT NULL,              -- endpoint ID
  finished_at INTEGER NOT NULL DEFAULT 0,    -- unix nanoseconds; 0: under way
  FOREIGN KEY (service, generation) REFERENCES generations(service, number)
);
CREATE INDEX rollouts_service ON rollouts(service, id);
-- Not in the design's table: a service has at most one rollout under way, and
-- the database refuses a second one whatever the code does.
CREATE UNIQUE INDEX rollouts_under_way ON rollouts(service)
  WHERE state IN ('preparing', 'activating');
