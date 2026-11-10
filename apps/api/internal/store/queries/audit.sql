-- Audit log (data-model.md, "audit_log"), written in the same transaction
-- as the change it records.

-- name: InsertAuditLog :exec
-- details never holds personal data (data-model.md, "audit_log").
insert into app.audit_log (tenant_id, actor_type, actor_id, action, subject_type, subject_id, request_id, details)
values (app.current_tenant_id(), 'api_key', @actor_id, @action, @subject_type, @subject_id, @request_id, (@details::text)::jsonb);
