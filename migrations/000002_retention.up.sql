-- Retention policy: keeps last 30 days by default.
-- Controlled by the retention CronJob in k8s/retention-cronjob.yaml.
-- This migration just creates the cleanup function.

CREATE OR REPLACE FUNCTION cleanup_old_telemetry(retain_days INTEGER DEFAULT 30)
RETURNS TABLE(deleted_metrics BIGINT, deleted_logs BIGINT, deleted_spans BIGINT)
LANGUAGE plpgsql AS $$
DECLARE
  cutoff TIMESTAMPTZ := NOW() - (retain_days || ' days')::INTERVAL;
  del_metrics BIGINT;
  del_logs    BIGINT;
  del_spans   BIGINT;
BEGIN
  DELETE FROM metrics WHERE timestamp < cutoff;
  GET DIAGNOSTICS del_metrics = ROW_COUNT;

  DELETE FROM logs WHERE timestamp < cutoff;
  GET DIAGNOSTICS del_logs = ROW_COUNT;

  DELETE FROM spans WHERE start_time < cutoff;
  GET DIAGNOSTICS del_spans = ROW_COUNT;

  RETURN QUERY SELECT del_metrics, del_logs, del_spans;
END;
$$;
