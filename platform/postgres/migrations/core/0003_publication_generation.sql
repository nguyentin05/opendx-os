-- SPDX-License-Identifier: Apache-2.0
-- Copyright 2026 DX-OS Open-Core Contributors
ALTER TABLE core.apps ADD COLUMN IF NOT EXISTS publication_generation BIGINT NOT NULL DEFAULT 0 CHECK (
    publication_generation >= 0
);

CREATE OR REPLACE FUNCTION core.claim_knowledge_publication(
    token UUID
) RETURNS JSONB
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog,
core,
public AS $$
DECLARE event core.outbox_events;publication jsonb;
BEGIN
 SELECT * INTO event FROM core.outbox_events WHERE event_type='knowledge.publication'
 AND ((delivery_status='pending' AND next_attempt_at<=now()) OR(delivery_status='in_flight' AND lease_until<=now()))
 ORDER BY created_at,id FOR UPDATE SKIP LOCKED LIMIT 1;
 IF NOT FOUND THEN RETURN NULL;END IF;
 UPDATE core.outbox_events SET delivery_status='in_flight',attempt_count=attempt_count+1,lease_token=token,lease_until=now()+interval '45 seconds' WHERE id=event.id;
 SELECT spec->'_publication' INTO publication FROM core.apps WHERE app_id=event.app_id;
 RETURN jsonb_build_object('event_id',event.id,'app_id',event.app_id,'publication_generation',publication->'generation','spec_digest',publication->'spec_digest');
END $$;
CREATE OR REPLACE FUNCTION core.ack_knowledge_publication(
    event UUID, token UUID, success BOOLEAN
) RETURNS BOOLEAN
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog,
core,
public AS $$
BEGIN
 UPDATE core.outbox_events SET delivery_status=CASE WHEN success THEN 'delivered' ELSE 'pending' END,lease_token=NULL,lease_until=NULL,
 delivered_at=CASE WHEN success THEN now() ELSE NULL END,next_attempt_at=now()+make_interval(secs=>least(300,5*power(2,least(attempt_count,6)))::double precision),last_error=CASE WHEN success THEN NULL ELSE 'KNOWLEDGE_DELIVERY_UNAVAILABLE' END
 WHERE id=event AND event_type='knowledge.publication' AND delivery_status='in_flight' AND lease_token=token AND lease_until>now();
 RETURN FOUND;
END $$;
REVOKE ALL ON FUNCTION core.claim_knowledge_publication(UUID),
core.ack_knowledge_publication(UUID, UUID, BOOLEAN) FROM public;
DO $$ BEGIN
 IF EXISTS(SELECT FROM pg_roles WHERE rolname='dxos_ai_dispatcher') THEN
  GRANT USAGE ON SCHEMA core TO dxos_ai_dispatcher;
  GRANT EXECUTE ON FUNCTION core.claim_knowledge_publication(uuid),core.ack_knowledge_publication(uuid,uuid,boolean) TO dxos_ai_dispatcher;
 END IF;
END $$;
