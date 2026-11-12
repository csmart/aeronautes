-- Why a bookmark did not verify after import (ADR 0114 decision 9): a
-- plain reason chosen by the kind of failure, never the error's text.
-- Cleared by a later successful connect or editor verify.
ALTER TABLE bookmarks ADD COLUMN verify_failure TEXT;
