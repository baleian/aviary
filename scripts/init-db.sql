CREATE EXTENSION IF NOT EXISTS "pgcrypto";

-- Dedicated DB for Keycloak (owned by aviary user; shares the same cluster
-- so we don't run a second postgres instance for identity).
CREATE DATABASE keycloak OWNER aviary;
