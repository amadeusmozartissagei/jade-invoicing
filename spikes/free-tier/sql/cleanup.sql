-- Spike only: stop the job and remove the test queue so the free quota is not
-- used after the spike.
select cron.unschedule('spike-drain');
select pgmq.drop_queue('spike');
