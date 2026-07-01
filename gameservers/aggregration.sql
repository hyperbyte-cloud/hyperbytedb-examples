-- Downsampling pipeline (Kapacitor → HyperByteDB)
--
-- Original Kapacitor task (every minute, aligned):
--   SELECT count(cpu) AS num_servers, sum(players), sum(bot_players), ...
--   FROM servercheck."default".serverstats
--   GROUP BY locationid, fleetid, regionid, provider, accountserviceid, ...
--   → servercheck.default_high.serverstats
--
-- HyperByteDB offers two ways to run the same rollup:
--
--   Continuous query  — cron-style; RESAMPLE EVERY 1m matches Kapacitor's
--                       `.period(1m)` + `.cron('* * * * *')`. Re-scans the
--                       window on each tick (up to ~10s scheduler latency).
--
--   Materialized view — incremental; aggregates on each flush to the source
--                       measurement (near real-time, no RESAMPLE clause).
--
-- Pick one approach for production; both examples below target the same shape.
--
-- Grafana sends ?rp=default from the datasource; HyperByteDB maps "default" to the
-- database default RP (autogen). Qualified FROM wins over ?rp=:
--   SELECT ... FROM "default_high"."server_stats"  → default_high
-- Or use the Gameservers (default_high) datasource (retentionPolicy: default_high).

CREATE DATABASE "gameservers"

USE "gameservers"

CREATE RETENTION POLICY "default_high" ON "gameservers" DURATION 52w REPLICATION 1

SHOW RETENTION POLICIES ON "gameservers"

-- ---------------------------------------------------------------------------
-- Option A: Continuous query (Kapacitor-style cron schedule)
-- ---------------------------------------------------------------------------

CREATE CONTINUOUS QUERY "cq_server_stats" ON "gameservers"
RESAMPLE EVERY 1m
BEGIN
  SELECT
    count("cpu") AS "num_servers",
    sum("players") AS "players",
    sum("max_players") AS "maxplayers",
    sum("cpu") AS "cpu",
    sum("mem") AS "mem",
    sum("used_slots") AS "usedslots"
  INTO "default_high"."server_stats"
  FROM "server_stats"
  GROUP BY time(1m),
    "location_id",
    "fleet_id",
    "region_id",
    "provider",
    "account_service_id",
    "profile_id",
    "game_id",
    "mod_id"
END

-- ---------------------------------------------------------------------------
-- Option B: Materialized view (incremental, flush-triggered)
-- ---------------------------------------------------------------------------
-- Player/resource rollups (no num_servers — use CQ below if you need count(cpu)).
-- Tags omitted from GROUP BY (e.g. server_id) are dropped from the destination series.
-- Query the rollup measurement, not raw server_stats in default_high:
--   SELECT sum("players") FROM "default_high"."server_stats_1m" GROUP BY time(1m), "region_id"

-- After upgrading HyperByteDB with rollup-aware MV merge + SummingMergeTree dest
-- storage, drop and recreate the MV so destination metadata and table engine are
-- correct and stale partial rows are cleared:
--   DROP MATERIALIZED VIEW "mv_server_stats_1m" ON "gameservers"
--   DROP MEASUREMENT "server_stats_1m"
-- then run the CREATE MATERIALIZED VIEW below again.

CREATE MATERIALIZED VIEW "mv_server_stats" ON "gameservers"
AS SELECT
  sum("players") AS "players",
  sum("max_players") AS "maxplayers",
  sum("cpu") AS "cpu",
  sum("mem") AS "mem",
  sum("used_slots") AS "usedslots"
INTO "default_high"."server_stats"
FROM "server_stats"
GROUP BY time(1m),
  "account_service_id",
  "fleet_id",
  "region",
  "region_id",
  "location_id",
  "provider",
  "profile_id",
  "game_id",
  "mod_id"


SHOW MATERIALIZED VIEWS ON "gameservers"
-- ---------------------------------------------------------------------------
-- Cleanup
-- ---------------------------------------------------------------------------

DROP CONTINUOUS QUERY "cq_server_stats" ON "gameservers"
DROP MATERIALIZED VIEW "mv_server_stats" ON "gameservers"
DROP MEASUREMENT "server_stats"
DROP RETENTION POLICY "default_high" ON "gameservers"
