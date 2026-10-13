-- A grant is a role that a key was given, for a service or for every
-- service. draal decides who may do what by these, and by nothing else
-- but the admins of its command line. A key has as many grants as it was
-- given: the three of id, role and service are what a grant is.
CREATE TABLE grants (
  id         TEXT    NOT NULL,              -- endpoint ID
  role       TEXT    NOT NULL,              -- 'admin', 'deployer' or 'zathras'
  service    TEXT    NOT NULL DEFAULT '',   -- '': every service, which an admin always is of
  note       TEXT    NOT NULL DEFAULT '',   -- what a person wrote beside it
  created_at INTEGER NOT NULL,              -- unix nanoseconds
  created_by TEXT    NOT NULL,              -- endpoint ID of the admin who granted it
  PRIMARY KEY (id, role, service)
);
