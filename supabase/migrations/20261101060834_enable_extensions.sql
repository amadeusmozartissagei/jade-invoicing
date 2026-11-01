-- Extensions for background work: scheduling (pg_cron), outbound HTTP from
-- the database (pg_net), and durable queues (pgmq).
--
-- Schemas match what the Supabase dashboard uses, so this migration is a
-- no-op on the existing remote project and reproduces it on a new one.
-- pgmq is created without "with schema": the extension creates its own
-- pgmq schema, and naming a schema that does not exist yet fails (3F000).

create extension if not exists pg_cron with schema pg_catalog;
create extension if not exists pg_net with schema extensions;
create extension if not exists pgmq;
