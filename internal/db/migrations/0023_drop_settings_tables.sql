-- The application settings live in a configuration file of their own
-- (ADR 0109 decision 1), and ui_preferences was never used.
DROP TABLE app_settings;
DROP TABLE ui_preferences;
