-- The active-job budget's default becomes two (ADR 0105 decision 6). Since
-- ADR 0047 enforced one, every save of any setting has written this key as
-- 1, so a stored 1 cannot be told from that forced value and is cleared for
-- the default to apply. A stored 2, 3 or 4 is a choice made before ADR 0047
-- and is kept; a value above 4 is left as written and reads as 4.
DELETE FROM app_settings WHERE key = 'max_active_jobs' AND value = '1';
