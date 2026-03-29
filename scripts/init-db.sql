-- Create separate database for Keycloak
CREATE DATABASE keycloak;
GRANT ALL PRIVILEGES ON DATABASE keycloak TO agentbox;

-- Enable uuid extension for agentbox database
CREATE EXTENSION IF NOT EXISTS "pgcrypto";
