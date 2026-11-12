-- What a segmented upload to the bookmark publishes (ADR 0111): 'slo' or
-- 'dlo'. Every existing row has none, which reads as static, the default,
-- through COALESCE in selectColumns.
ALTER TABLE bookmarks ADD COLUMN large_objects TEXT;
