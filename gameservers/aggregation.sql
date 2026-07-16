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

CREATE RETENTION POLICY "default_high" ON "multiplay" DURATION 52w REPLICATION 1

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
-- Player/resource rollups + num_servers = count("cpu").
-- Groups by every tag except machine_id and server_id (the only high-cardinality
-- tags), so those two are dropped from the destination series; everything else is
-- preserved. Derive averages in Grafana from the stored sums + num_servers:
--   Avg CPU %    = sum("cpu")/sum("num_servers")
--   Avg memory   = sum("mem")/sum("num_servers")
--   Slot util %  = sum("used_slots")/sum("max_players")*100
-- Do NOT use mean() inside an MV on HyperByteDB <= 0.8.2: the sum/count expansion
-- nests aggregates and chDB rejects it (Code 184 ILLEGAL_AGGREGATION). Fixed in
-- 0.8.3 (commit 8c6ed8a); until deployed, store sums and divide at query time.
-- Query the rollup measurement, not raw server_stats in default_high:
--   SELECT sum("players") FROM "default_high"."server_stats_1m" GROUP BY time(1m), "region_id"

-- After upgrading HyperByteDB with rollup-aware MV merge + SummingMergeTree dest
-- storage, drop and recreate the MV so destination metadata and table engine are
-- correct and stale partial rows are cleared:
--   DROP MATERIALIZED VIEW "mv_server_stats_1m" ON "gameservers"
--   DROP MEASUREMENT "server_stats_1m"
-- then run the CREATE MATERIALIZED VIEW below again.

-- Default: fast create; rollups start from the next server_stats write.
-- Add WITH BACKFILL before AS to scan existing history (can take minutes on
-- large measurements — run during a maintenance window on the Raft leader).

CREATE MATERIALIZED VIEW "mv_server_stats" ON "multiplay"
AS SELECT
  count("cpu") AS "num_servers",
  sum("players") AS "players",
  sum("max_players") AS "max_players",
  sum("used_slots") AS "used_slots"
INTO "default_high"."server_stats"
FROM "server_stats"
GROUP BY time(1m),
  "account_service_id",
  "fleet",
  "fleet_id",
  "game_id",
  "location_id",
  "map",
  "mod_id",
  "profile_id",
  "provider",
  "region",
  "region_id"

-- To backfill existing server_stats history on create:
-- CREATE MATERIALIZED VIEW "mv_server_stats" ON "multiplay" WITH BACKFILL
-- AS SELECT ...


SHOW MATERIALIZED VIEWS ON "gameservers"
-- ---------------------------------------------------------------------------
-- Cleanup
-- ---------------------------------------------------------------------------

DROP CONTINUOUS QUERY "cq_server_stats" ON "gameservers"
DROP MATERIALIZED VIEW "mv_server_stats" ON "gameservers"
DROP MEASUREMENT "server_stats"
DROP RETENTION POLICY "default_high" ON "gameservers"
