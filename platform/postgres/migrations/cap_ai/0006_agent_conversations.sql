-- SPDX-License-Identifier: Apache-2.0
-- Copyright 2026 DX-OS Open-Core Contributors

CREATE TABLE cap_ai.agent_conversations (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_user_id uuid NOT NULL REFERENCES core.users(id),
    title text NOT NULL CHECK (length(trim(title)) BETWEEN 1 AND 512),
    state text NOT NULL DEFAULT 'ACTIVE' CHECK (state IN ('ACTIVE', 'DELETED')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    last_seq bigint NOT NULL DEFAULT 0 CHECK (last_seq >= 0)
);
CREATE INDEX agent_conversations_owner ON cap_ai.agent_conversations(owner_user_id, id DESC) WHERE state = 'ACTIVE';

CREATE TABLE cap_ai.agent_messages (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    conversation_id uuid NOT NULL REFERENCES cap_ai.agent_conversations(id),
    seq bigint NOT NULL CHECK (seq > 0),
    role text NOT NULL CHECK (role IN ('USER', 'ASSISTANT')),
    content text NOT NULL,
    response jsonb,
    status text NOT NULL CHECK (status IN ('PENDING', 'COMPLETE')),
    client_key text,
    body_hash text,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (conversation_id, seq),
    UNIQUE (conversation_id, client_key),
    CHECK ((role = 'USER' AND client_key IS NOT NULL AND body_hash IS NOT NULL AND response IS NULL)
        OR (role = 'ASSISTANT' AND client_key IS NULL AND body_hash IS NULL AND status = 'COMPLETE' AND jsonb_typeof(response) = 'object'))
);
CREATE UNIQUE INDEX agent_one_pending ON cap_ai.agent_messages(conversation_id) WHERE status = 'PENDING';

-- These are immutable source identity snapshots, intentionally without source FKs:
-- source purges must not erase transcript provenance or be blocked by history.
CREATE TABLE cap_ai.agent_message_sources (
    message_id uuid NOT NULL REFERENCES cap_ai.agent_messages(id),
    source_order integer NOT NULL CHECK (source_order >= 0),
    app_id text NOT NULL CHECK (length(trim(app_id)) > 0),
    scope_kind text NOT NULL CHECK (scope_kind IN ('APP', 'PERSONAL')),
    acting_role text NOT NULL CHECK (length(trim(acting_role)) > 0),
    document_id uuid NOT NULL,
    version_id uuid NOT NULL,
    chunk_id uuid NOT NULL,
    citation_label text NOT NULL CHECK (length(trim(citation_label)) > 0),
    scope_snapshot jsonb NOT NULL CHECK (jsonb_typeof(scope_snapshot) = 'object'),
    PRIMARY KEY (message_id, source_order)
);

CREATE FUNCTION cap_ai.agent_active(actor uuid) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, cap_ai AS $$
BEGIN
  PERFORM 1 FROM core.users WHERE id = actor AND status = 'ACTIVE';
  IF NOT FOUND THEN RAISE EXCEPTION 'unavailable' USING ERRCODE = 'P0404'; END IF;
END $$;

CREATE FUNCTION cap_ai.agent_create_conversation(actor uuid, title text) RETURNS jsonb
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, cap_ai AS $$
DECLARE c cap_ai.agent_conversations;
BEGIN
  PERFORM cap_ai.agent_active(actor);
  IF title IS NULL OR length(trim(title)) NOT BETWEEN 1 AND 512 THEN RAISE EXCEPTION 'invalid title' USING ERRCODE = 'P0400'; END IF;
  INSERT INTO cap_ai.agent_conversations(owner_user_id,title) VALUES(actor,title) RETURNING * INTO c;
  RETURN to_jsonb(c);
END $$;

CREATE FUNCTION cap_ai.agent_list_conversations(actor uuid, limit_count integer, before_id uuid) RETURNS jsonb
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, cap_ai AS $$
DECLARE result jsonb;
BEGIN
  PERFORM cap_ai.agent_active(actor);
  IF limit_count IS NULL OR limit_count NOT BETWEEN 1 AND 100 THEN RAISE EXCEPTION 'invalid page' USING ERRCODE = 'P0400'; END IF;
  SELECT coalesce(jsonb_agg(to_jsonb(c) ORDER BY c.id DESC), '[]'::jsonb) INTO result FROM
    (SELECT * FROM cap_ai.agent_conversations WHERE owner_user_id = actor AND state = 'ACTIVE' AND (before_id IS NULL OR id < before_id) ORDER BY id DESC LIMIT limit_count) c;
  RETURN result;
END $$;

CREATE FUNCTION cap_ai.agent_get_messages(actor uuid, conversation uuid, limit_count integer, after_seq bigint) RETURNS jsonb
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, cap_ai AS $$
DECLARE result jsonb;
BEGIN
  PERFORM cap_ai.agent_active(actor);
  IF limit_count IS NULL OR limit_count NOT BETWEEN 1 AND 100 OR after_seq IS NULL OR after_seq < 0 THEN RAISE EXCEPTION 'invalid page' USING ERRCODE = 'P0400'; END IF;
  PERFORM 1 FROM cap_ai.agent_conversations WHERE id = conversation AND owner_user_id = actor AND state = 'ACTIVE';
  IF NOT FOUND THEN RAISE EXCEPTION 'unavailable' USING ERRCODE = 'P0404'; END IF;
  SELECT coalesce(jsonb_agg(to_jsonb(m) || jsonb_build_object('sources', (SELECT coalesce(jsonb_agg(to_jsonb(s) ORDER BY s.source_order),'[]'::jsonb) FROM cap_ai.agent_message_sources s WHERE s.message_id = m.id)) ORDER BY m.seq),'[]'::jsonb) INTO result
    FROM (SELECT * FROM cap_ai.agent_messages WHERE conversation_id = conversation AND seq > after_seq ORDER BY seq LIMIT limit_count) m;
  RETURN result;
END $$;

CREATE FUNCTION cap_ai.agent_begin_turn(actor uuid, conversation uuid, client_key text, body_hash text, query text) RETURNS jsonb
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, cap_ai AS $$
DECLARE c cap_ai.agent_conversations; m cap_ai.agent_messages; answer cap_ai.agent_messages;
BEGIN
  PERFORM cap_ai.agent_active(actor);
  IF client_key IS NULL OR length(client_key) NOT BETWEEN 1 AND 200 OR body_hash IS NULL OR length(body_hash) NOT BETWEEN 1 AND 256 OR query IS NULL OR length(trim(query)) NOT BETWEEN 1 AND 32768 THEN RAISE EXCEPTION 'invalid turn' USING ERRCODE = 'P0400'; END IF;
  SELECT * INTO c FROM cap_ai.agent_conversations WHERE id = conversation AND owner_user_id = actor AND state = 'ACTIVE' FOR UPDATE;
  IF NOT FOUND THEN RAISE EXCEPTION 'unavailable' USING ERRCODE = 'P0404'; END IF;
  SELECT * INTO m FROM cap_ai.agent_messages WHERE conversation_id = conversation AND agent_messages.client_key = agent_begin_turn.client_key;
  IF FOUND THEN
    IF m.body_hash <> body_hash OR m.content <> query THEN RAISE EXCEPTION 'conflict' USING ERRCODE = 'P0409'; END IF;
    SELECT * INTO answer FROM cap_ai.agent_messages WHERE conversation_id = conversation AND seq = m.seq + 1 AND role = 'ASSISTANT';
    RETURN to_jsonb(m) || jsonb_build_object('completion', CASE WHEN answer.id IS NULL THEN NULL ELSE to_jsonb(answer) END);
  END IF;
  IF EXISTS (SELECT 1 FROM cap_ai.agent_messages WHERE conversation_id = conversation AND status = 'PENDING') THEN RAISE EXCEPTION 'turn in progress' USING ERRCODE = 'P0409'; END IF;
  UPDATE cap_ai.agent_conversations SET last_seq = last_seq + 1, updated_at = now() WHERE id = conversation RETURNING * INTO c;
  INSERT INTO cap_ai.agent_messages(conversation_id,seq,role,content,status,client_key,body_hash) VALUES(conversation,c.last_seq,'USER',query,'PENDING',client_key,body_hash) RETURNING * INTO m;
  RETURN to_jsonb(m) || jsonb_build_object('completion', NULL);
END $$;

CREATE FUNCTION cap_ai.agent_finish_turn(actor uuid, user_message uuid, response jsonb) RETURNS jsonb
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, cap_ai AS $$
DECLARE c cap_ai.agent_conversations; m cap_ai.agent_messages; answer cap_ai.agent_messages; source jsonb; n integer := 0; payload jsonb;
BEGIN
  PERFORM cap_ai.agent_active(actor);
  SELECT conv.* INTO c FROM cap_ai.agent_conversations conv JOIN cap_ai.agent_messages msg ON msg.conversation_id = conv.id WHERE msg.id = user_message AND msg.role = 'USER' AND conv.owner_user_id = actor AND conv.state = 'ACTIVE' FOR UPDATE OF conv;
  IF NOT FOUND THEN RAISE EXCEPTION 'unavailable' USING ERRCODE = 'P0404'; END IF;
  SELECT * INTO m FROM cap_ai.agent_messages WHERE id = user_message;
  IF m.status = 'COMPLETE' THEN
    SELECT * INTO answer FROM cap_ai.agent_messages WHERE conversation_id = c.id AND seq = m.seq + 1 AND role = 'ASSISTANT';
    RETURN to_jsonb(answer);
  END IF;
  IF response IS NULL OR jsonb_typeof(response) <> 'object' OR jsonb_typeof(response->'response') IS DISTINCT FROM 'object' OR jsonb_typeof(response->'sources') IS DISTINCT FROM 'array' OR coalesce(jsonb_typeof(response->'response'->'answer'), '') NOT IN ('string', 'null') OR jsonb_array_length(response->'sources') > 100 THEN RAISE EXCEPTION 'invalid response' USING ERRCODE = 'P0400'; END IF;
  payload := response->'response';
  UPDATE cap_ai.agent_conversations SET last_seq = last_seq + 1, updated_at = now() WHERE id = c.id RETURNING * INTO c;
  INSERT INTO cap_ai.agent_messages(conversation_id,seq,role,content,response,status) VALUES(c.id,c.last_seq,'ASSISTANT',coalesce(payload->>'answer', ''),payload,'COMPLETE') RETURNING * INTO answer;
  FOR source IN SELECT value FROM jsonb_array_elements(response->'sources') LOOP
    INSERT INTO cap_ai.agent_message_sources(message_id,source_order,app_id,scope_kind,acting_role,document_id,version_id,chunk_id,citation_label,scope_snapshot)
    VALUES(answer.id,n,source->>'app_id',source->>'scope_kind',source->>'acting_role',(source->>'document_id')::uuid,(source->>'version_id')::uuid,(source->>'chunk_id')::uuid,source->>'citation_label',source->'scope_snapshot');
    n := n + 1;
  END LOOP;
  UPDATE cap_ai.agent_messages SET status = 'COMPLETE' WHERE id = m.id;
  RETURN to_jsonb(answer);
END $$;

CREATE FUNCTION cap_ai.agent_delete_conversation(actor uuid, conversation uuid) RETURNS jsonb
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, cap_ai AS $$
BEGIN
  PERFORM cap_ai.agent_active(actor);
  UPDATE cap_ai.agent_conversations SET state = 'DELETED', updated_at = now() WHERE id = conversation AND owner_user_id = actor AND state = 'ACTIVE';
  IF NOT FOUND THEN RAISE EXCEPTION 'unavailable' USING ERRCODE = 'P0404'; END IF;
  RETURN jsonb_build_object('deleted',true);
END $$;

ALTER TABLE cap_ai.agent_conversations OWNER TO dxos_ai_owner;
ALTER TABLE cap_ai.agent_messages OWNER TO dxos_ai_owner;
ALTER TABLE cap_ai.agent_message_sources OWNER TO dxos_ai_owner;
ALTER FUNCTION cap_ai.agent_active(uuid) OWNER TO dxos_ai_owner;
ALTER FUNCTION cap_ai.agent_create_conversation(uuid,text) OWNER TO dxos_ai_owner;
ALTER FUNCTION cap_ai.agent_list_conversations(uuid,integer,uuid) OWNER TO dxos_ai_owner;
ALTER FUNCTION cap_ai.agent_get_messages(uuid,uuid,integer,bigint) OWNER TO dxos_ai_owner;
ALTER FUNCTION cap_ai.agent_begin_turn(uuid,uuid,text,text,text) OWNER TO dxos_ai_owner;
ALTER FUNCTION cap_ai.agent_finish_turn(uuid,uuid,jsonb) OWNER TO dxos_ai_owner;
ALTER FUNCTION cap_ai.agent_delete_conversation(uuid,uuid) OWNER TO dxos_ai_owner;
REVOKE ALL ON cap_ai.agent_conversations, cap_ai.agent_messages, cap_ai.agent_message_sources FROM PUBLIC;
REVOKE ALL ON FUNCTION cap_ai.agent_active(uuid), cap_ai.agent_create_conversation(uuid,text), cap_ai.agent_list_conversations(uuid,integer,uuid), cap_ai.agent_get_messages(uuid,uuid,integer,bigint), cap_ai.agent_begin_turn(uuid,uuid,text,text,text), cap_ai.agent_finish_turn(uuid,uuid,jsonb), cap_ai.agent_delete_conversation(uuid,uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION cap_ai.agent_create_conversation(uuid,text), cap_ai.agent_list_conversations(uuid,integer,uuid), cap_ai.agent_get_messages(uuid,uuid,integer,bigint), cap_ai.agent_begin_turn(uuid,uuid,text,text,text), cap_ai.agent_finish_turn(uuid,uuid,jsonb), cap_ai.agent_delete_conversation(uuid,uuid) TO dxos_ai_api;
